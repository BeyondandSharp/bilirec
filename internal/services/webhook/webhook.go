package webhook

import (
	"context"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/configfile"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/utils"
	"github.com/go-resty/resty/v2"
	"github.com/puzpuzpuz/xsync/v4"
	"go.uber.org/fx"
	"golang.org/x/sync/semaphore"
)

var log = logger.Named("webhook")

const maxAttempts = 3

// Service emits BililiveRecorder-compatible Webhook v2 JSON to configured URLs.
type Service struct {
	urlsMu    sync.RWMutex
	urls      []string
	outputDir string
	client    *resty.Client

	rooms             *xsync.Map[int, *roomQueue]
	pending           chan roomWork
	deliverConcurrent *semaphore.Weighted
	ctx               context.Context
	cancel            context.CancelFunc
	workerWg          sync.WaitGroup

	convertMetaMu sync.Mutex
	convertMeta   map[string]*SegmentMeta
}

func NewService(lc fx.Lifecycle, cfg *config.Config) *Service {
	urls := config.ParseWebhookURLs(cfg.WebhookURLs)
	ctx, cancel := context.WithCancel(context.Background())
	client := resty.New().
		SetTimeout(15*time.Second).
		SetHeader("Content-Type", "application/json").
		SetHeader("User-Agent", "bilirec/webhook")
	s := &Service{
		urls:              urls,
		outputDir:         cfg.OutputDir,
		client:            client,
		rooms:             xsync.NewMap[int, *roomQueue](),
		pending:           make(chan roomWork, pendingWorkCap),
		deliverConcurrent: semaphore.NewWeighted(int64(maxConcurrentDelivers)),
		ctx:               ctx,
		cancel:            cancel,
		convertMeta:       make(map[string]*SegmentMeta),
	}

	lc.Append(fx.StartStopHook(
		func() error {
			s.workerWg.Go(s.runWorker)
			return nil
		},
		func() error {
			s.cancel()
			s.workerWg.Wait()
			return nil
		},
	))
	return s
}

// SetURLs replaces the active webhook targets at runtime.
func (s *Service) SetURLs(raw string) {
	if s == nil {
		return
	}
	urls := config.ParseWebhookURLs(raw)
	s.urlsMu.Lock()
	s.urls = urls
	s.urlsMu.Unlock()
}

// BindConfigStore subscribes to /config changes so WEBHOOK_URLS applies
// without restarting the process.
func (s *Service) BindConfigStore(store *configfile.Store) {
	if s == nil || store == nil {
		return
	}
	store.OnConfigChange(func(change configfile.ConfigChange) {
		if change.Key != "WEBHOOK_URLS" {
			return
		}
		if change.Present {
			s.SetURLs(change.Value)
			return
		}
		s.SetURLs("")
	})
}

func (s *Service) urlsSnapshot() []string {
	if s == nil {
		return nil
	}
	s.urlsMu.RLock()
	defer s.urlsMu.RUnlock()
	return append([]string(nil), s.urls...)
}

func (s *Service) enabled() bool {
	return s != nil && len(s.urlsSnapshot()) > 0 && s.client != nil
}

func (s *Service) emit(eventType EventType, data roomEventData) {
	if !s.enabled() {
		return
	}
	eventID, err := utils.NewUUIDv4()
	if err != nil {
		log.Warnf("webhook 生成 EventId 失败：%v", err)
		return
	}
	body := envelope{
		EventType:      eventType,
		EventTimestamp: time.Now().Format(time.RFC3339Nano),
		EventID:        eventID,
		EventData:      data,
	}
	s.enqueue(data.RoomID, body)
}

func (s *Service) deliver(ctx context.Context, body envelope) {
	var wg sync.WaitGroup
	for _, url := range s.urlsSnapshot() {
		wg.Go(func() {
			s.deliverURL(ctx, url, body)
		})
	}
	wg.Wait()
}

func (s *Service) deliverURL(ctx context.Context, url string, body envelope) {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return
		}
		resp, err := s.client.R().
			SetContext(ctx).
			SetBody(body).
			Post(url)
		if err == nil && resp != nil && resp.StatusCode() >= 200 && resp.StatusCode() < 300 {
			break
		}
		if err != nil {
			log.Warnf("webhook POST %s 失败（%d/%d）：%v", url, attempt, maxAttempts, err)
		} else if resp != nil {
			log.Warnf("webhook POST %s 非 2xx（%d/%d）：%d", url, attempt, maxAttempts, resp.StatusCode())
		}
		if attempt == maxAttempts {
			break
		}
		if !sleepBackoff(ctx, time.Duration(attempt)*time.Second) {
			return
		}
	}
}

func sleepBackoff(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

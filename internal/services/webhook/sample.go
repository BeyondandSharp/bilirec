package webhook

import (
	"context"
	"errors"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/utils"
)

// ErrNotConfigured is returned when no WEBHOOK_URLS are configured.
var ErrNotConfigured = errors.New("未配置 webhook 地址")

// TestResult is the delivery result for one configured webhook URL.
type TestResult struct {
	URL    string `json:"url"`
	OK     bool   `json:"ok"`
	Status int    `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
}

// TestReport is the result of a sample webhook test.
type TestReport struct {
	Sent      bool         `json:"sent"`
	EventType EventType    `json:"event_type"`
	EventID   string       `json:"event_id"`
	Results   []TestResult `json:"results"`
}

// SendSample sends one complete FileClosed sample event to every configured
// webhook URL synchronously. It deliberately bypasses the per-room queue so the
// settings UI gets immediate success/failure feedback.
func (s *Service) SendSample(ctx context.Context) (*TestReport, error) {
	if s == nil || !s.enabled() {
		return nil, ErrNotConfigured
	}
	eventID, err := utils.NewUUIDv4()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	room := &bilibili.LiveRoomInfoDetail{
		RoomID:         123456,
		ShortID:        123456,
		Uname:          "测试主播",
		Title:          "测试直播标题",
		ParentAreaName: "生活",
		AreaName:       "影音馆",
		LiveStatus:     1,
	}
	data := baseRoomData(room, "test-session-"+eventID, true, true)
	data.RelativePath = "123456-测试主播/20260101_120000_测试直播标题.flv"
	data.FileOpenTime = now.Add(-60 * time.Second).Format(time.RFC3339Nano)
	data.FileCloseTime = now.Format(time.RFC3339Nano)
	data.FileSize = 10 * 1024 * 1024
	data.Duration = 60
	body := envelope{
		EventType:      EventFileClosed,
		EventTimestamp: now.Format(time.RFC3339Nano),
		EventID:        eventID,
		EventData:      data,
	}

	report := &TestReport{
		Sent:      true,
		EventType: EventFileClosed,
		EventID:   eventID,
		Results:   make([]TestResult, 0, len(s.urls)),
	}
	for _, target := range s.urls {
		result := TestResult{URL: target}
		resp, postErr := s.client.R().
			SetContext(ctx).
			SetBody(body).
			Post(target)
		switch {
		case postErr != nil:
			result.Error = postErr.Error()
		case resp == nil:
			result.Error = "empty response"
		default:
			result.Status = resp.StatusCode()
			result.OK = resp.StatusCode() >= 200 && resp.StatusCode() < 300
			if !result.OK {
				result.Error = resp.String()
			}
		}
		report.Results = append(report.Results, result)
	}
	return report, nil
}

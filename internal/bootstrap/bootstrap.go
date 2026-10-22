package bootstrap

import (
	"os"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/controllers/auth"
	configapi "github.com/bilirec/bilirec/internal/controllers/config"
	"github.com/bilirec/bilirec/internal/controllers/convert"
	"github.com/bilirec/bilirec/internal/controllers/file"
	lg "github.com/bilirec/bilirec/internal/controllers/logs"
	nc "github.com/bilirec/bilirec/internal/controllers/notify"
	"github.com/bilirec/bilirec/internal/controllers/record"
	"github.com/bilirec/bilirec/internal/controllers/room"
	cs "github.com/bilirec/bilirec/internal/controllers/settings"
	whc "github.com/bilirec/bilirec/internal/controllers/webhook"
	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/modules/configfile"
	"github.com/bilirec/bilirec/internal/modules/logging"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/internal/modules/rest"
	co "github.com/bilirec/bilirec/internal/services/convert"
	cv "github.com/bilirec/bilirec/internal/services/cover"
	dm "github.com/bilirec/bilirec/internal/services/danmaku"
	ex "github.com/bilirec/bilirec/internal/services/expose"
	fi "github.com/bilirec/bilirec/internal/services/file"
	ja "github.com/bilirec/bilirec/internal/services/janitor"
	me "github.com/bilirec/bilirec/internal/services/media"
	no "github.com/bilirec/bilirec/internal/services/notify"
	pa "github.com/bilirec/bilirec/internal/services/path"
	re "github.com/bilirec/bilirec/internal/services/recorder"
	rp "github.com/bilirec/bilirec/internal/services/repair"
	ro "github.com/bilirec/bilirec/internal/services/room"
	se "github.com/bilirec/bilirec/internal/services/settings"
	st "github.com/bilirec/bilirec/internal/services/stream"
	sc "github.com/bilirec/bilirec/internal/services/subcheck"
	su "github.com/bilirec/bilirec/internal/services/subscribe"
	wh "github.com/bilirec/bilirec/internal/services/webhook"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/updatecheck"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"
)

func MainModule(stores ...*configfile.Store) fx.Option {
	var store *configfile.Store
	if len(stores) > 0 {
		store = stores[0]
	}
	return fx.Module("main",
		fx.Provide(func() *configfile.Store { return store }),
		config.Module,
		bilibili.Module,
		rest.Module,
		metrics.Module,
		logging.Module,

		fx.Provide(pa.NewService),
		fx.Provide(co.NewService),
		fx.Provide(me.NewService),
		fx.Provide(dm.NewService),
		fx.Provide(st.NewService),
		fx.Provide(cv.NewService),
		fx.Provide(rp.NewService),
		fx.Provide(se.NewService),
		fx.Provide(re.NewService),
		fx.Provide(ro.NewService),
		fx.Provide(su.NewService),
		fx.Provide(no.NewService),
		fx.Provide(wh.NewService),
		fx.Provide(fi.NewService),

		fx.Invoke(ja.NewService),
		fx.Invoke(ex.NewService),
		fx.Invoke(sc.NewService),

		fx.Invoke(updatecheck.InvokeCheck),

		fx.Invoke(room.NewController),
		fx.Invoke(cs.NewController),
		fx.Invoke(configapi.NewController),
		fx.Invoke(lg.NewController),
		fx.Invoke(nc.NewController),
		fx.Invoke(record.NewController),
		fx.Invoke(file.NewController),
		fx.Invoke(auth.NewController),
		fx.Invoke(convert.NewController),
		fx.Invoke(whc.NewController),
	)
}

func noQrCodePrompt() bool {
	loginMode := strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("BILIBILI_LOGIN_MODE"), "controller")))
	return loginMode == "controller" || loginMode == "anonymous"
}

func NewApp(stores ...*configfile.Store) *fx.App {
	return fx.New(
		MainModule(stores...),
		fx.StartTimeout(
			utils.Ternary(
				noQrCodePrompt(),
				15*time.Second,
				1*time.Minute,
			),
		),
		fx.StopTimeout(1*time.Minute),
		fx.WithLogger(fxZapLogger),
	)
}

func NewAndroidApp(stores ...*configfile.Store) *fx.App {
	return fx.New(
		MainModule(stores...),
		fx.StartTimeout(10*time.Second),
		fx.StopTimeout(10*time.Second),
		fx.WithLogger(fxZapLogger),
	)
}

func fxZapLogger() fxevent.Logger {
	return &fxevent.ZapLogger{
		Logger: logger.Named("fx").Zap().WithOptions(zap.IncreaseLevel(zap.WarnLevel)),
	}
}

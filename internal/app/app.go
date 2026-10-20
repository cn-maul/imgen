// Package app 负责依赖装配与进程生命周期。
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/imgen/imgen/internal/blob"
	"github.com/imgen/imgen/internal/config"
	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/httpx"
	"github.com/imgen/imgen/internal/httpapi"
	"github.com/imgen/imgen/internal/janitor"
	"github.com/imgen/imgen/internal/job"
	"github.com/imgen/imgen/internal/provider"
	"github.com/imgen/imgen/internal/provider/agnes"
	"github.com/imgen/imgen/internal/provider/sensenova"
	"github.com/imgen/imgen/internal/secret"
	"github.com/imgen/imgen/internal/store"
	"github.com/imgen/imgen/internal/web"
)

// Version 与 BuildTime 由 main 通过 ldflags 注入。
var (
	Version   = "dev"
	BuildTime = "unknown"
)

// App 持有全部运行期依赖。
type App struct {
	Cfg   config.Config
	Log   *slog.Logger
	St    *store.Store
	Blobs *blob.Store
	Cipher *secret.Cipher
	Keys  job.KeyResolver
	Provs map[domain.ProviderID]provider.Provider
	Exec  *job.Executor
	Q     *job.Queue
	Jobs  *job.Manager
	HTTP  *httpx.Client
	// API 是 httpapi 实例，由 Handler 惰性构造；janitor 从它读取运行期 keepDays。
	API *httpapi.API
	// Janitor 按保留期清理到期数据；Sweep 暴露给测试与手工触发。
	Janitor *janitor.Janitor
	Server  *http.Server
}

// Run 装配并启动服务，阻塞至收到退出信号。
func Run(cfg config.Config) error {
	log := newLogger(cfg)
	a, err := New(cfg, log)
	if err != nil {
		return err
	}
	defer a.Close()
	return a.Serve()
}

// New 完成全部依赖装配但不监听端口，便于测试。
func New(cfg config.Config, log *slog.Logger) (*App, error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}

	cipher, err := secret.NewCipherFromEnv(cfg.MasterKey, cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("初始化密钥: %w", err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}

	blobs := blob.NewStore(cfg.BlobDir, cfg.ThumbDir, cfg.MaxRemoteMB)
	keys := &keyResolver{st: st, cipher: cipher}

	provs := map[domain.ProviderID]provider.Provider{}
	for id, p := range map[domain.ProviderID]provider.Provider{
		domain.ProviderSenseNova: sensenova.New(""),
		domain.ProviderAgnes:     agnes.New(""),
	} {
		provs[id] = p
	}

	exec := &job.Executor{
		Providers: provs,
		Keys:      keys,
		Blobs:     blobs,
		St:        st,
		Log:       log,
		HTTP:      httpx.New(log),
	}
	q := job.NewQueue(cfg.Concurrency)
	mgr := job.NewManager(exec, st, q, log)

	a := &App{
		Cfg: cfg, Log: log, St: st, Blobs: blobs, Cipher: cipher,
		Keys: keys, Provs: provs, Exec: exec, Q: q, Jobs: mgr, HTTP: exec.HTTP,
	}
	return a, nil
}

// Handler 返回完整的 HTTP 路由：/api/v1 由 httpapi 提供，其余走前端。
func (a *App) Handler() (http.Handler, error) {
	api, err := newAPI(a)
	if err != nil {
		return nil, err
	}
	a.API = api
	if a.Janitor == nil {
		a.Janitor = janitor.New(a.St, a.Blobs, janitor.Options{
			KeepDays: api.KeepDays,
			Log:      a.Log,
		})
	}
	static, err := web.Handler(web.Boot{
		Version:     Version,
		BuildTime:   BuildTime,
		MaxUploadMB: a.Cfg.MaxUploadMB,
		Concurrency: a.Q.Workers(),
	})
	if err != nil {
		return nil, fmt.Errorf("加载前端: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler())
	mux.Handle("/", static)
	return mux, nil
}

// Serve 监听并优雅退出。
func (a *App) Serve() error {
	h, err := a.Handler()
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              a.Cfg.Addr,
		Handler:           h,
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       5 * time.Minute, // 上传可能很慢
		WriteTimeout:      0,               // SSE 需要长连接，不设写超时
		IdleTimeout:       120 * time.Second,
	}
	a.Server = srv

	// 清理任务随服务一起起停：先 Start（首轮立即扫一次），退出时先 Stop。
	stopJanitor := func() {}
	if a.Janitor != nil {
		stopJanitor = a.Janitor.Start()
	}

	errCh := make(chan error, 1)
	go func() {
		a.Log.Info("Imgen 已启动", "addr", a.Cfg.Addr, "version", Version, "data", a.Cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		a.Log.Info("收到退出信号，正在关闭", "signal", sig.String())
	}

	// 先停队列（不再接新活），再给存量任务一个收尾窗口。
	stopJanitor()
	a.Q.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("关闭服务: %w", err)
	}
	return nil
}

func (a *App) Close() {
	if a.Janitor != nil {
		a.Janitor.Stop()
	}
	if a.Q != nil {
		a.Q.Close()
	}
	if a.St != nil {
		_ = a.St.Close()
	}
}

func newLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("IMGEN_DEBUG") != "" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

// keyResolver 组合 store 与 secret：读密文 → 解密 → 明文。
type keyResolver struct {
	st     *store.Store
	cipher *secret.Cipher
}

func (k *keyResolver) APIKey(_ context.Context, id domain.ProviderID) (string, error) {
	enc, err := k.st.GetProviderKey(id)
	if err != nil {
		return "", err
	}
	plain, err := k.cipher.Decrypt(enc)
	if err != nil {
		return "", fmt.Errorf("%w: 通道 %s 的 Key 解密失败", domain.ErrNoCredential, id)
	}
	return string(plain), nil
}

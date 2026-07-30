package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Audi-dask/Overseer/internal/api"
	"github.com/Audi-dask/Overseer/internal/auth"
	"github.com/Audi-dask/Overseer/internal/notify"
	"github.com/Audi-dask/Overseer/internal/ocr/stdout"
	"github.com/Audi-dask/Overseer/internal/pipeline"
	"github.com/Audi-dask/Overseer/internal/queue"
	"github.com/Audi-dask/Overseer/internal/runlog"
	"github.com/Audi-dask/Overseer/internal/secretbox"
	"github.com/Audi-dask/Overseer/internal/store"
	"github.com/Audi-dask/Overseer/internal/ui"
	"github.com/Audi-dask/Overseer/internal/vcs/gitlab"
)

func main() {
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}

	box, err := secretbox.NewFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join("data", "overseer.db")
	}
	st, err := store.Open(dbPath, box)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	// Agent / pipeline output goes to per-review log files; terminal keeps HTTP access logs only.
	stdout.SetWriter(io.Discard)
	if v := os.Getenv("REVIEW_LOG_DIR"); v != "" {
		runlog.SetDir(v)
	} else {
		runlog.SetDir(filepath.Join(filepath.Dir(dbPath), "reviewlogs"))
	}

	if n, err := st.ResolveStaleRunning(context.Background()); err == nil && n > 0 {
		log.Printf("startup: %d 条中断的审查记录标记为 failed", n)
	}
	if needSetup, err := st.BootstrapAdminIfNeeded(context.Background()); err != nil {
		log.Fatal(err)
	} else if needSetup {
		log.Printf("startup: 管理员未初始化，请打开管理台完成首次设置（或设置环境变量 ADMIN_PASSWORD）")
	}

	settings, _ := st.GetSettings(context.Background())
	debounce := 30 * time.Second
	concurrency := 8
	if settings != nil {
		if settings.DebounceSec > 0 {
			debounce = time.Duration(settings.DebounceSec) * time.Second
		}
		if settings.MaxConcurrency > 0 {
			concurrency = settings.MaxConcurrency
		}
	}

	authSvc := auth.New(nil)
	notifier := notify.New()
	runner := &pipeline.Runner{
		Store:  st,
		GL:     gitlab.New(),
		Notify: notifier,
	}
	q := queue.New(debounce, concurrency, runner.Handle)
	srv := &api.Server{
		Store:  st,
		Queue:  q,
		Runner: runner,
		Notify: notifier,
		Box:    box,
		Auth:   authSvc,
	}

	mux := http.NewServeMux()
	srv.Register(mux)
	mux.Handle("/", ui.Handler())

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.Printf("overseer listening on http://localhost%s (db=%s, debounce=%s, concurrency=%d)",
		addr, dbPath, debounce, concurrency)
	handler := api.Logging(srv.AuthMiddleware(mux))
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}

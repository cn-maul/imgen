// Package janitor 按保留期清理到期的任务与无人引用的资产。
//
// 这是「历史保留天数」设置（keepDays）的唯一执行者：settings 只负责保存用户
// 的意图，真正让数据过期的逻辑在这里。没有它，UI 上「到期任务与未被引用的
// 资产会被清理任务删除」这句话就是空承诺。
//
// 设计取向与项目其余部分一致：能简单就不复杂。
//   - 不引入独立调度框架，用 time.Ticker；
//   - 不做增量游标，每次全量扫描（单机 SQLite、量级在万行以内）；
//   - 删除是尽力而为：单个文件删不掉只记 warn，不中断整轮扫描。
package janitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/imgen/imgen/internal/blob"
	"github.com/imgen/imgen/internal/store"
)

// DefaultInterval 是两轮扫描之间的间隔。
//
// 保留期的精度是天，扫描频率取小时级足够；再密集只是白烧 CPU。
const DefaultInterval = time.Hour

// Options 是清理任务的参数。
type Options struct {
	// KeepDays 返回当前保留天数；每次扫描都重新读取，因此设置改完下一轮即生效。
	// 返回 0 表示用户关闭了清理，本次扫描直接跳过。
	KeepDays func() int
	// Interval 为扫描间隔，<=0 时取 DefaultInterval。
	Interval time.Duration
	// Now 可在测试中替换时间源。
	Now func() time.Time
	Log *slog.Logger
}

// Janitor 周期清理过期数据。
type Janitor struct {
	st       *store.Store
	blobs    *blob.Store
	opts     Options
	stop     chan struct{}
	done     chan struct{}
	started  bool
}

// New 构造清理任务。st 必须非 nil；blobs 可为 nil（此时只删库记录，不动磁盘）。
func New(st *store.Store, blobs *blob.Store, opts Options) *Janitor {
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Janitor{
		st:    st,
		blobs: blobs,
		opts:  opts,
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
}

// Stats 是一轮清理的结果，便于日志与测试断言。
type Stats struct {
	JobsDeleted   int
	AssetsDeleted int
	FilesRemoved  int
	FileErrors    int
}

// Start 在后台启动周期扫描：立即先跑一轮，随后按 Interval 重复。
// 返回的函数用于停止（幂等，可重复调用）。
func (j *Janitor) Start() func() {
	if j.started {
		return j.Stop
	}
	j.started = true
	go func() {
		defer close(j.done)

		j.sweepLogged(context.Background())
		t := time.NewTicker(j.opts.Interval)
		defer t.Stop()
		for {
			select {
			case <-j.stop:
				return
			case <-t.C:
				j.sweepLogged(context.Background())
			}
		}
	}()
	return j.Stop
}

// Stop 通知后台协程退出并等待其结束。
func (j *Janitor) Stop() {
	select {
	case <-j.stop:
	default:
		close(j.stop)
	}
	// 未 Start 过就无需等待。
	if !j.started {
		return
	}
	<-j.done
}

func (j *Janitor) sweepLogged(ctx context.Context) {
	stats, err := j.Sweep(ctx)
	if err != nil {
		j.opts.Log.Warn("清理任务失败", "err", err)
		return
	}
	if stats.JobsDeleted == 0 && stats.AssetsDeleted == 0 {
		// 无事可做时不刷日志，避免每小时一条噪音。
		return
	}
	j.opts.Log.Info("清理完成",
		"jobs", stats.JobsDeleted,
		"assets", stats.AssetsDeleted,
		"files", stats.FilesRemoved,
		"fileErrors", stats.FileErrors)
}

// Sweep 执行一轮清理并返回统计。ctx 目前只用于提前退出，可为 nil。
func (j *Janitor) Sweep(ctx context.Context) (Stats, error) {
	var stats Stats
	var keepDays int
	if j.opts.KeepDays != nil {
		keepDays = j.opts.KeepDays()
	}
	if keepDays <= 0 {
		// 0 表示不清理；负数按同一语义处理。
		return stats, nil
	}
	if err := ctx.Err(); err != nil {
		return stats, err
	}

	cutoff := j.opts.Now().AddDate(0, 0, -keepDays)

	// 顺序要紧：先删任务，它们对资产的引用才会消失，
	// 下一句才能把失去引用的资产判为孤儿。
	jobs, err := j.st.PruneTerminalJobs(cutoff)
	if err != nil {
		return stats, err
	}
	stats.JobsDeleted = jobs

	if err := ctx.Err(); err != nil {
		return stats, err
	}

	victims, err := j.st.PruneUnreferencedAssets(cutoff)
	if err != nil {
		return stats, err
	}
	stats.AssetsDeleted = len(victims)

	// 库记录已删，磁盘文件删不掉只会留垃圾，不影响一致性。
	if j.blobs == nil {
		return stats, nil
	}
	for _, a := range victims {
		if a.Path == "" {
			continue
		}
		if err := j.blobs.Remove(a.Path); err != nil {
			stats.FileErrors++
			j.opts.Log.Warn("删除资产文件失败", "asset", a.ID, "path", a.Path, "err", err)
			continue
		}
		stats.FilesRemoved++
	}
	return stats, nil
}

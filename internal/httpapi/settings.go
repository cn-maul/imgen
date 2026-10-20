package httpapi

import (
	"net/http"
)

// getSettings 回显当前生效的设置。
// concurrency 来自 Queue（权威运行期值），其余来自本服务的运行期状态。
func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, settingsDTO{
		Concurrency: a.currentConcurrency(),
		MaxUploadMB: a.MaxUploadMB(),
		KeepDays:    a.KeepDays(),
		QueueDepth:  a.queueDepth(),
	})
}

// putSettings 更新设置。
//
// 支持的键：concurrency（1..16，写回 Queue）、maxUploadMB、keepDays。
// 未出现的键保持不变；值非法时整体拒绝（400），不做部分写入。
func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	var req putSettingsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Concurrency == nil && req.MaxUploadMB == nil && req.KeepDays == nil {
		writeDomainErr(w, errBadRequest("没有需要更新的设置项"))
		return
	}

	// 先整体校验，避免改了一半才失败。
	if req.Concurrency != nil {
		if *req.Concurrency < minConcurrency || *req.Concurrency > maxConcurrency {
			writeDomainErr(w, errBadRequest(
				"concurrency 必须在 1..16 之间"))
			return
		}
	}
	if req.MaxUploadMB != nil {
		if *req.MaxUploadMB < minUploadMB || *req.MaxUploadMB > maxUploadMBCap {
			writeDomainErr(w, errBadRequest(
				"maxUploadMB 必须在 1..512 之间"))
			return
		}
	}
	if req.KeepDays != nil {
		if *req.KeepDays < minKeepDays || *req.KeepDays > maxKeepDays {
			writeDomainErr(w, errBadRequest(
				"keepDays 必须在 0..365 之间"))
			return
		}
	}

	// ① 并发上限：立即生效，改动影响后续入队，不打断存量任务。
	if req.Concurrency != nil && a.deps.Jobs != nil && a.deps.Jobs.Q != nil {
		a.deps.Jobs.Q.SetWorkers(*req.Concurrency)
	}
	// ② 上传上限与保留天数：改内存状态，并落 kv 供重启后恢复。
	if req.MaxUploadMB != nil {
		a.setMaxUploadMB(*req.MaxUploadMB)
	}
	if req.KeepDays != nil {
		a.setKeepDays(*req.KeepDays)
	}

	// ③ 持久化。失败的键写日志但不回滚内存值 —— 设置已经生效，
	//    重启后退回默认比「改了没生效」更可预期。
	a.persist(req)

	writeJSON(w, http.StatusOK, settingsMutationDTO{Settings: settingsDTO{
		Concurrency: a.currentConcurrency(),
		MaxUploadMB: a.MaxUploadMB(),
		KeepDays:    a.KeepDays(),
		QueueDepth:  a.queueDepth(),
	}})
}

// persist 把已生效的设置写进 kv。
func (a *API) persist(req putSettingsRequest) {
	if a.deps.St == nil {
		return
	}
	if req.Concurrency != nil {
		if err := a.deps.St.SetKV(kvConcurrency, *req.Concurrency); err != nil {
			a.log.Warn("持久化 concurrency 失败", "err", err)
		}
	}
	if req.MaxUploadMB != nil {
		if err := a.deps.St.SetKV(kvMaxUploadMB, *req.MaxUploadMB); err != nil {
			a.log.Warn("持久化 maxUploadMB 失败", "err", err)
		}
	}
	if req.KeepDays != nil {
		if err := a.deps.St.SetKV(kvKeepDays, *req.KeepDays); err != nil {
			a.log.Warn("持久化 keepDays 失败", "err", err)
		}
	}
}

// currentConcurrency 返回队列的当前并发上限，缺失时回落到 1。
func (a *API) currentConcurrency() int {
	if a.deps.Jobs == nil || a.deps.Jobs.Q == nil {
		return minConcurrency
	}
	n := a.deps.Jobs.Q.Workers()
	if n < minConcurrency {
		return minConcurrency
	}
	return n
}

// queueDepth 返回当前排队数，供前端显示水位。
func (a *API) queueDepth() int {
	if a.deps.Jobs == nil || a.deps.Jobs.Q == nil {
		return 0
	}
	return a.deps.Jobs.Q.Len()
}

// kvInt 读取一个整数设置。GetKV(key, def) 会把 JSON 反序列化进 def 指针，
// 所以这里传 *int 并在成功时取回。ok=false 表示未持久化或解析失败。
//
// ⚠️ 哨兵值不可省：键不存在时 GetKV 直接原样返回 def 指针（不报错），
// 若默认取 0 会被误判成「已持久化为 0」，进而把 concurrency 钳到 1、
// keepDays 归零。故用 -1 表示「未设置」——三个设置项的下界均 ≥0，不会撞车。
func (a *API) kvInt(key string) (int, bool) {
	if a.deps.St == nil {
		return 0, false
	}
	def := kvUnset
	got, err := a.deps.St.GetKV(key, &def)
	if err != nil {
		a.log.Warn("读取设置失败", "key", key, "err", err)
		return 0, false
	}
	v, ok := got.(*int)
	if !ok || v == nil || *v == kvUnset {
		return 0, false
	}
	return *v, true
}

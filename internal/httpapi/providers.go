package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/imgen/imgen/internal/domain"
	"github.com/imgen/imgen/internal/httpx"
	"github.com/imgen/imgen/internal/provider"
	"github.com/imgen/imgen/internal/secret"
	"github.com/imgen/imgen/internal/store"
)

// listProviders 返回两家通道的模型、能力矩阵与配置状态。
//
// 前端靠这份数据驱动参数面板显隐，字段名必须稳定。
// 模型表以适配器的 Models() 为权威静态表：SQLite 的 models 表只作为种子，
// 代码改了模型但没人改迁移时，这里也不会漂移。
func (a *API) listProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := a.deps.St.ListProviders()
	if err != nil {
		writeErr(w, err)
		return
	}

	masks := a.keyMasks()
	out := make([]providerDTO, 0, len(rows))
	for _, row := range rows {
		dto := providerDTO{
			ID:         string(row.ID),
			Name:       row.Name,
			BaseURL:    row.BaseURL,
			Enabled:    row.Enabled,
			HasKey:     row.HasKey,
			APIKeyMask: masks[row.ID],
			TimeoutMS:  int(row.Timeout / time.Millisecond),
			CreatedAt:  row.CreatedAt,
		}

		p, ok := a.deps.Providers[row.ID]
		if !ok {
			// 通道在 DB 里但适配器没装配（如二期才有的 openai）：
			// 仍要出现在列表上，能力与模型为空集合而非报错。
			dto.Models = []domain.Model{}
			dto.Caps = map[string]provider.Caps{}
			out = append(out, dto)
			continue
		}
		if dto.Name == "" {
			dto.Name = p.Name()
		}

		models := p.Models()
		dto.Models = models
		if dto.Models == nil {
			dto.Models = []domain.Model{}
		}
		dto.Caps = make(map[string]provider.Caps, len(models))
		for _, m := range models {
			dto.Caps[m.ID] = p.Capabilities(m.ID)
		}
		out = append(out, dto)
	}

	writeJSON(w, http.StatusOK, providerListDTO{Providers: out})
}

// patchProvider 更新一家通道的 baseUrl / apiKey / enabled / timeoutMs。
// 未知 id → 404 NOT_FOUND（:id 参数校验要求）。
func (a *API) patchProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := a.requireProvider(domain.ProviderID(id)); err != nil {
		writeDomainErr(w, err)
		return
	}

	var req patchProviderRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.BaseURL == nil && req.APIKey == nil && req.Enabled == nil && req.TimeoutMS == nil {
		writeDomainErr(w, errBadRequest("没有需要更新的字段"))
		return
	}

	cfg := store.ProviderConfig{Enabled: req.Enabled, TimeoutMS: req.TimeoutMS}
	if req.BaseURL != nil {
		u := strings.TrimSpace(*req.BaseURL)
		if u == "" {
			writeDomainErr(w, errBadRequest("baseUrl 不能为空"))
			return
		}
		cfg.BaseURL = u
	}
	switch {
	case req.APIKey != nil && strings.TrimSpace(*req.APIKey) == "":
		// 空字符串表示清除 Key；与「字段未出现」区分开。
		cfg.ClearKey = true
	case req.APIKey != nil:
		if a.deps.Cipher == nil {
			writeDomainErr(w, internalNoCipher())
			return
		}
		enc, err := a.deps.Cipher.Encrypt([]byte(strings.TrimSpace(*req.APIKey)))
		if err != nil {
			// Encrypt 对空值报错，上面已过滤；这里只可能是密钥设施故障。
			writeDomainErr(w, errInternal("加密 API Key 失败", err))
			return
		}
		cfg.APIKey = enc
	}

	if err := a.deps.St.UpdateProvider(domain.ProviderID(id), cfg); err != nil {
		writeErr(w, err)
		return
	}

	dto, err := a.providerDTO(domain.ProviderID(id))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, providerMutationDTO{Provider: *dto})
}

// testProvider 探测一家通道的连通性：真实发一次最小生成请求。
//
// 「连通」的判定比成功更宽：拿到任何 HTTP 响应（哪怕是 401/429）都说明
// 网络与端点可达且凭据通路正常；只有网络层失败才算不通。
func (a *API) testProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	pid := domain.ProviderID(id)
	if err := a.requireProvider(pid); err != nil {
		writeDomainErr(w, err)
		return
	}

	var req testProviderRequest
	// 请求体可选：仅有默认值时 Body 为空也能通过。
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}

	p, ok := a.deps.Providers[pid]
	if !ok {
		writeDomainErr(w, errNotFound(fmt.Sprintf("通道 %s 未装配适配器", id)))
		return
	}

	model := pickModel(strings.TrimSpace(req.Model), p)
	if model == "" {
		writeDomainErr(w, errBadRequest(fmt.Sprintf("通道 %s 没有可用模型", id)))
		return
	}

	// 未配 Key 时不发请求：省掉一次必然失败的往返。
	if a.deps.Keys != nil {
		if _, err := a.deps.Keys.APIKey(r.Context(), pid); err != nil {
			writeDomainErr(w, domain.NewError(domain.CodeBadRequest,
				"尚未配置 API Key，请先在设置页填写"))
			return
		}
	}

	out := a.probe(r.Context(), pid, p, model, req.TimeoutSec)
	writeJSON(w, http.StatusOK, out)
}

// probe 向一家通道发一次最小请求并归一化结果。
func (a *API) probe(ctx context.Context, pid domain.ProviderID, p provider.Provider, model string, timeoutSec int) testProviderDTO {
	now := time.Now().UTC()
	res := testProviderDTO{Model: model, CheckedAt: now}

	if a.deps.Keys == nil {
		res.OK = false
		res.Error = domain.NewError(domain.CodeInternal, "密钥解析器未装配")
		res.Message = res.Error.Message
		return res
	}
	key, err := a.deps.Keys.APIKey(ctx, pid)
	if err != nil {
		res.OK = false
		res.Error = toDomainError(err)
		res.Message = res.Error.Message
		return res
	}
	baseURL, err := a.providerBaseURL(pid)
	if err != nil {
		res.OK = false
		res.Error = toDomainError(err)
		res.Message = res.Error.Message
		return res
	}

	timeout := time.Duration(p.Capabilities(model).TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	// 探测不该占用太久：默认再夹到 30s 上限。
	if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	if timeoutSec > 0 {
		timeout = time.Duration(timeoutSec) * time.Second
	}

	// 极简请求：空 prompt 无法生成，但足以让上游给出鉴权/限流/参数响应，
	// 从而证明链路可达。
	payload, perr := p.Translate(domain.Request{
		Provider: pid, Model: model, Prompt: "ping",
		Mode: domain.ModeTxt2Img, Aspect: domain.Aspect1_1, Tier: domain.Tier1K, Count: 1,
	}, nil)
	if perr != nil {
		res.OK = false
		res.Error = toDomainError(perr)
		res.Message = res.Error.Message
		return res
	}

	body, err := marshalPayload(payload.Body)
	if err != nil {
		res.OK = false
		res.Error = domain.NewError(domain.CodeInternal, "构造探测请求失败")
		res.Message = res.Error.Message
		return res
	}

	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+key)
	hdr.Set("Content-Type", "application/json")
	hdr.Set("Accept", "application/json")
	for k, v := range payload.Header {
		hdr.Set(k, v)
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	resp, err := a.up.Do(cctx, &httpx.Call{
		Method:      payload.Method,
		URL:         strings.TrimRight(baseURL, "/") + payload.Path,
		Header:      hdr,
		Body:        func() ([]byte, error) { return body, nil },
		RetryPolicy: httpx.RetryNever, // 探测只求一次真实响应
	})
	res.LatencyMS = time.Since(start).Milliseconds()
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	if err != nil {
		res.OK = false
		res.Error = toDomainError(err)
		res.Message = res.Error.Message
		return res
	}
	if resp == nil {
		res.OK = false
		res.Error = domain.NewError(domain.CodeUpstreamUnavailable, "上游未返回任何响应")
		res.Message = res.Error.Message
		return res
	}

	res.Status = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		res.OK = true
		res.Message = "端点可达"
		return res
	}

	// 拿到 HTTP 状态码即证明链路通；状态码本身交给适配器分类。
	raw, _ := httpx.ReadBody(resp.Body, 1<<20)
	de := p.Classify(resp.StatusCode, raw)
	res.Error = de
	res.Message = de.Message
	// 401/403 意味着 Key 有问题而不是连不上；其余一律视为「可达但被拒」。
	reachable := true
	if de != nil {
		switch de.Code {
		case domain.CodeUpstreamAuth:
			reachable = false
		}
	}
	res.OK = reachable
	return res
}

// requireProvider 校验 provider :id 参数，未知 id 返回 404。
// 先看 DB 里有没有这条通道记录，再确认适配器已装配。
func (a *API) requireProvider(id domain.ProviderID) *domain.Error {
	if id == "" {
		return errBadRequest("缺少通道 id")
	}
	rows, err := a.deps.St.ListProviders()
	if err != nil {
		return errInternal("读取通道配置失败", err)
	}
	for _, row := range rows {
		if row.ID == id {
			return nil
		}
	}
	return errNotFound(fmt.Sprintf("未知通道 %s", id))
}

// providerDTO 组装单条通道回显（含掩码）。
func (a *API) providerDTO(id domain.ProviderID) (*providerDTO, error) {
	rows, err := a.deps.St.ListProviders()
	if err != nil {
		return nil, err
	}
	var row *store.Provider
	for i := range rows {
		if rows[i].ID == id {
			row = &rows[i]
			break
		}
	}
	if row == nil {
		return nil, errNotFound(fmt.Sprintf("未知通道 %s", id))
	}

	dto := &providerDTO{
		ID:         string(row.ID),
		Name:       row.Name,
		BaseURL:    row.BaseURL,
		Enabled:    row.Enabled,
		HasKey:     row.HasKey,
		APIKeyMask: a.keyMask(row.ID),
		TimeoutMS:  int(row.Timeout / time.Millisecond),
		CreatedAt:  row.CreatedAt,
	}
	p, ok := a.deps.Providers[row.ID]
	if ok {
		if dto.Name == "" {
			dto.Name = p.Name()
		}
		models := p.Models()
		dto.Models = models
		dto.Caps = make(map[string]provider.Caps, len(models))
		for _, m := range models {
			dto.Caps[m.ID] = p.Capabilities(m.ID)
		}
	}
	if dto.Models == nil {
		dto.Models = []domain.Model{}
	}
	if dto.Caps == nil {
		dto.Caps = map[string]provider.Caps{}
	}
	return dto, nil
}

// providerBaseURL 读取一家通道的配置地址，并检查是否已启用。
func (a *API) providerBaseURL(id domain.ProviderID) (string, error) {
	rows, err := a.deps.St.ListProviders()
	if err != nil {
		return "", err
	}
	for _, p := range rows {
		if p.ID != id {
			continue
		}
		if !p.Enabled {
			return "", domain.NewError(domain.CodeBadRequest, fmt.Sprintf("通道 %s 已禁用", id))
		}
		return p.BaseURL, nil
	}
	return "", fmt.Errorf("%w: 通道 %s", domain.ErrNotFound, id)
}

// keyMasks 批量解密各通道 Key 并掩码。
// 解密失败不致命：那只影响回显，Key 本身仍在 DB 里。
func (a *API) keyMasks() map[domain.ProviderID]string {
	out := map[domain.ProviderID]string{}
	if a.deps.St == nil || a.deps.Cipher == nil {
		return out
	}
	rows, err := a.deps.St.ListProviders()
	if err != nil {
		a.log.Warn("读取通道列表失败", "err", err)
		return out
	}
	for _, row := range rows {
		if !row.HasKey {
			continue
		}
		out[row.ID] = a.keyMask(row.ID)
	}
	return out
}

// keyMask 解密单个通道的 Key 并返回掩码。
func (a *API) keyMask(id domain.ProviderID) string {
	if a.deps.Cipher == nil {
		return ""
	}
	enc, err := a.deps.St.GetProviderKey(id)
	if err != nil {
		if !errors.Is(err, domain.ErrNoCredential) {
			a.log.Warn("读取通道密钥失败", "provider", id, "err", err)
		}
		return ""
	}
	plain, err := a.deps.Cipher.Decrypt(enc)
	if err != nil {
		a.log.Warn("解密通道密钥失败（主密钥已变更？）", "provider", id, "err", err)
		return ""
	}
	return secret.Mask(string(plain))
}

// pickModel 选择探测用模型：指定值优先，回落到该通道第一个模型。
func pickModel(want string, p provider.Provider) string {
	models := p.Models()
	if want != "" {
		for _, m := range models {
			if m.ID == want {
				return m.ID
			}
		}
		// 指定但不在静态表里：仍照发，让上游给出权威判定。
		return want
	}
	if len(models) > 0 {
		return models[0].ID
	}
	return ""
}

func internalNoCipher() *domain.Error {
	return domain.NewError(domain.CodeInternal, "加密设施未装配，无法保存 API Key")
}

// marshalPayload 序列化探测请求体。
func marshalPayload(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("序列化探测请求: %w", err)
	}
	return raw, nil
}

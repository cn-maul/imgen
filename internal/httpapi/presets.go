package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/imgen/imgen/internal/store"
)

// 预设名与分类的长度上限。
//
// 预设 body 以 JSON 整体存储，上限由 maxJSONBody（1MB）兜底；
// 这里只额外约束用户可见的短字段，避免列表页被超长文本撑破。
const (
	maxPresetNameLen = 80
	maxPresetMetaLen = 500
)

// presetDTO 是预设的对外形状。Body 保持 json.RawMessage 透传，
// 不在传输层反序列化成结构体 —— 它的字段属于前端模板语义，
// 后端没有理由知道或校验（真正的校验发生在提交生成任务时）。
type presetDTO struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Body        json.RawMessage `json:"body"`
	CreatedAt   string          `json:"createdAt"`
	UpdatedAt   string          `json:"updatedAt"`
}

type presetListDTO struct {
	Presets []presetDTO `json:"presets"`
}

type presetMutationDTO struct {
	Preset presetDTO `json:"preset"`
}

// presetRequest 是创建与更新的请求体。
//
// 两个动作共用一个 DTO：预设字段少、总是一起编辑，
// 拆 POST/PATCH 只会让「哪些字段必填」变模糊。
type presetRequest struct {
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Body        json.RawMessage `json:"body"`
}

// validate 把请求体收敛为可落库的预设，并拒绝空名 / 超长字段。
func (req presetRequest) validate() (store.PromptPreset, error) {
	name := store.NormalizePresetName(req.Name)
	if name == "" {
		return store.PromptPreset{}, errBadRequest("预设名称不能为空")
	}
	if len([]rune(name)) > maxPresetNameLen {
		return store.PromptPreset{}, errBadRequest("预设名称过长")
	}
	category := strings.TrimSpace(req.Category)
	description := strings.TrimSpace(req.Description)
	if len([]rune(category)) > maxPresetNameLen {
		return store.PromptPreset{}, errBadRequest("分类名称过长")
	}
	if len([]rune(description)) > maxPresetMetaLen {
		return store.PromptPreset{}, errBadRequest("描述过长")
	}
	// body 允许缺省（等价 {}），但必须是合法 JSON 对象，
	// 否则前端下次读回来会拿到无法解析的结构。
	body := req.Body
	if len(body) == 0 {
		body = json.RawMessage(`{}`)
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		return store.PromptPreset{}, errBadRequest("body 不是合法的 JSON 对象: " + err.Error())
	}
	return store.PromptPreset{
		Name:        name,
		Category:    category,
		Description: description,
		Body:        body,
	}, nil
}

func toPresetDTO(p store.PromptPreset) presetDTO {
	body := p.Body
	if len(body) == 0 {
		body = json.RawMessage(`{}`)
	}
	return presetDTO{
		ID:          p.ID,
		Name:        p.Name,
		Category:    p.Category,
		Description: p.Description,
		Body:        body,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

// listPresets 处理 GET /api/v1/presets。
func (a *API) listPresets(w http.ResponseWriter, r *http.Request) {
	if a.deps.St == nil {
		writeDomainErr(w, errInternal("未接入存储", nil))
		return
	}
	list, err := a.deps.St.ListPresets()
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]presetDTO, 0, len(list))
	for _, p := range list {
		out = append(out, toPresetDTO(p))
	}
	writeJSON(w, http.StatusOK, presetListDTO{Presets: out})
}

// createPreset 处理 POST /api/v1/presets。
func (a *API) createPreset(w http.ResponseWriter, r *http.Request) {
	if a.deps.St == nil {
		writeDomainErr(w, errInternal("未接入存储", nil))
		return
	}
	var req presetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := req.validate()
	if err != nil {
		writeErr(w, err)
		return
	}
	created, err := a.deps.St.CreatePreset(p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, presetMutationDTO{Preset: toPresetDTO(*created)})
}

// getPreset 处理 GET /api/v1/presets/:id。
func (a *API) getPreset(w http.ResponseWriter, r *http.Request) {
	if a.deps.St == nil {
		writeDomainErr(w, errInternal("未接入存储", nil))
		return
	}
	p, err := a.deps.St.GetPreset(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, presetMutationDTO{Preset: toPresetDTO(*p)})
}

// updatePreset 处理 PUT /api/v1/presets/:id。
func (a *API) updatePreset(w http.ResponseWriter, r *http.Request) {
	if a.deps.St == nil {
		writeDomainErr(w, errInternal("未接入存储", nil))
		return
	}
	var req presetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	p, err := req.validate()
	if err != nil {
		writeErr(w, err)
		return
	}
	updated, err := a.deps.St.UpdatePreset(chi.URLParam(r, "id"), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, presetMutationDTO{Preset: toPresetDTO(*updated)})
}

// deletePreset 处理 DELETE /api/v1/presets/:id。
func (a *API) deletePreset(w http.ResponseWriter, r *http.Request) {
	if a.deps.St == nil {
		writeDomainErr(w, errInternal("未接入存储", nil))
		return
	}
	if err := a.deps.St.DeletePreset(chi.URLParam(r, "id")); err != nil {
		writeErr(w, err)
		return
	}
	writeNoContent(w)
}

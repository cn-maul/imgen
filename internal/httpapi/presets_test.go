package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodePresets 解析列表信封。
func decodePresets(t *testing.T, body []byte) []presetDTO {
	t.Helper()
	var out presetListDTO
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("解析列表失败: %v\n%s", err, body)
	}
	return out.Presets
}

// TestPresetsCRUDOverHTTP 覆盖 CRUD 全链路。
//
// 注意库里有迁移 0004 播种的内置海报预设，所以「初始状态」不是空列表；
// 下面的断言都按「1 条内置 + 本测试自建的 1 条」来计算。
func TestPresetsCRUDOverHTTP(t *testing.T) {
	e := newTestEnv(t)

	// 初始列表只含内置预设，且必须是 [] 而非 null，前端可直接遍历。
	w := e.do(t, "GET", "/api/v1/presets", nil)
	if w.Code != 200 {
		t.Fatalf("GET /presets = %d", w.Code)
	}
	initial := decodePresets(t, w.Body.Bytes())
	if len(initial) != 1 || initial[0].ID != builtinPosterPresetID {
		t.Fatalf("初始列表应只含内置预设 %s: %+v", builtinPosterPresetID, initial)
	}
	if !strings.Contains(w.Body.String(), `"presets":[{`) {
		t.Errorf("非空列表应为数组而非 null: %s", w.Body.String())
	}

	// 创建
	w = e.do(t, "POST", "/api/v1/presets", []byte(`{
		"name":"  人像换背景 ",
		"category":"换背景",
		"description":"保留人物，换掉背景",
		"body":{"prompt":"把 @2 的人物放进 @1 的场景"}
	}`))
	if w.Code != 201 {
		t.Fatalf("POST /presets = %d: %s", w.Code, w.Body.String())
	}
	var mut presetMutationDTO
	if err := json.Unmarshal(w.Body.Bytes(), &mut); err != nil {
		t.Fatalf("解析创建响应失败: %v", err)
	}
	id := mut.Preset.ID
	if id == "" {
		t.Fatal("创建响应缺少 id")
	}
	if mut.Preset.Name != "人像换背景" {
		t.Errorf("名称未去空白: %q", mut.Preset.Name)
	}

	// body 必须原样透传：后端不解释模板字段。
	if !strings.Contains(string(mut.Preset.Body), "@2") {
		t.Errorf("body 未透传: %s", mut.Preset.Body)
	}

	// 读单条
	w = e.do(t, "GET", "/api/v1/presets/"+id, nil)
	if w.Code != 200 {
		t.Fatalf("GET /presets/%s = %d", id, w.Code)
	}

	// 更新
	w = e.do(t, "PUT", "/api/v1/presets/"+id, []byte(`{
		"name":"人像换背景 v2",
		"body":{"prompt":"把 @1 的人物放进 @2 的场景"}
	}`))
	if w.Code != 200 {
		t.Fatalf("PUT /presets/%s = %d: %s", id, w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &mut); err != nil {
		t.Fatalf("解析更新响应失败: %v", err)
	}
	if mut.Preset.Name != "人像换背景 v2" {
		t.Errorf("更新后名称 = %q", mut.Preset.Name)
	}

	// 列表里应能看到（外加那条内置预设）。
	// 列表按 updated_at 倒序，本测试刚更新过自己这条，所以它排在前面。
	w = e.do(t, "GET", "/api/v1/presets", nil)
	list := decodePresets(t, w.Body.Bytes())
	if len(list) != 2 {
		t.Fatalf("列表长度 = %d, want 2（内置 1 + 自建 1）: %+v", len(list), list)
	}
	if list[0].ID != id {
		t.Errorf("刚更新过的预设应排在最前: %+v", list)
	}

	// 删除
	w = e.do(t, "DELETE", "/api/v1/presets/"+id, nil)
	if w.Code != 204 {
		t.Fatalf("DELETE /presets/%s = %d", id, w.Code)
	}
	w = e.do(t, "GET", "/api/v1/presets/"+id, nil)
	if w.Code != 404 {
		t.Errorf("删除后 GET = %d, want 404", w.Code)
	}
	// 删除自建项后，内置预设必须仍在。
	w = e.do(t, "GET", "/api/v1/presets", nil)
	if list = decodePresets(t, w.Body.Bytes()); len(list) != 1 || list[0].ID != builtinPosterPresetID {
		t.Errorf("删除自建项后应只剩内置预设: %+v", list)
	}
}

func TestPresetsRejectBadInput(t *testing.T) {
	e := newTestEnv(t)

	cases := []struct {
		name string
		body string
	}{
		{"空名", `{"name":"   ","body":{}}`},
		{"缺 name", `{"body":{}}`},
		{"body 不是对象", `{"name":"x","body":[1,2,3]}`},
		{"body 非法 JSON", `{"name":"x","body":"oops"}`},
		{"名称过长", `{"name":"` + strings.Repeat("啊", 200) + `"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := e.do(t, "POST", "/api/v1/presets", []byte(c.body))
			if w.Code != 400 {
				t.Errorf("状态码 = %d, want 400: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestPresetsMissingBodyDefaultsToObject 保证 body 缺省时落库为 {}。
func TestPresetsMissingBodyDefaultsToObject(t *testing.T) {
	e := newTestEnv(t)
	w := e.do(t, "POST", "/api/v1/presets", []byte(`{"name":"只有名字"}`))
	if w.Code != 201 {
		t.Fatalf("状态码 = %d: %s", w.Code, w.Body.String())
	}
	var mut presetMutationDTO
	_ = json.Unmarshal(w.Body.Bytes(), &mut)
	if string(mut.Preset.Body) != `{}` {
		t.Errorf("body = %s, want {}", mut.Preset.Body)
	}
}

func TestPresetsNotFound(t *testing.T) {
	e := newTestEnv(t)
	if w := e.do(t, "GET", "/api/v1/presets/nope", nil); w.Code != 404 {
		t.Errorf("GET 未知 id = %d, want 404", w.Code)
	}
	if w := e.do(t, "PUT", "/api/v1/presets/nope", []byte(`{"name":"x"}`)); w.Code != 404 {
		t.Errorf("PUT 未知 id = %d, want 404", w.Code)
	}
	if w := e.do(t, "DELETE", "/api/v1/presets/nope", nil); w.Code != 404 {
		t.Errorf("DELETE 未知 id = %d, want 404", w.Code)
	}
}

// builtinPosterPresetID 与迁移 0004_builtin_poster_preset.sql 里的固定 id 对应。
//
// 内置预设 id 必须是确定值：随机 id 会让同一份迁移在不同库上产出不同 id，
// 前端无法引用，回归测试也无从断言。
const builtinPosterPresetID = "p_builtin_poster_split"

// TestBuiltinPosterPresetIsSeeded 锁住内置「上下分区艺术海报」预设的形状。
//
// 这条预设由迁移播种，改迁移内容必须同步改这里，否则说明是无意识的漂移。
func TestBuiltinPosterPresetIsSeeded(t *testing.T) {
	e := newTestEnv(t)

	w := e.do(t, "GET", "/api/v1/presets", nil)
	if w.Code != 200 {
		t.Fatalf("GET /presets = %d", w.Code)
	}
	var found *presetDTO
	for _, p := range decodePresets(t, w.Body.Bytes()) {
		if p.ID == builtinPosterPresetID {
			cp := p
			found = &cp
			break
		}
	}
	if found == nil {
		t.Fatalf("内置海报预设 %s 未被播种", builtinPosterPresetID)
	}

	if found.Name != "城市风景 · 上下分区艺术海报" {
		t.Errorf("name = %q", found.Name)
	}
	if found.Category != "艺术海报" {
		t.Errorf("category = %q", found.Category)
	}

	// body 是前端模板语义，后端只透传；这里断言的是「字段是否齐」，
	// 因为缺 aspect/tier 会让预设落进工作台时用默认值而非海报要求的值。
	var body struct {
		Prompt          string            `json:"prompt"`
		Refs            []json.RawMessage `json:"refs"`
		Aspect          string            `json:"aspect"`
		Tier            string            `json:"tier"`
		IncludeRefNotes bool              `json:"includeRefNotes"`
	}
	if err := json.Unmarshal(found.Body, &body); err != nil {
		t.Fatalf("解析 body 失败: %v\n%s", err, found.Body)
	}
	if body.Aspect != "2:3" {
		t.Errorf("aspect = %q, want 2:3", body.Aspect)
	}
	if body.Tier != "2K" {
		t.Errorf("tier = %q, want 2K", body.Tier)
	}
	if !body.IncludeRefNotes {
		t.Error("includeRefNotes 应为 true")
	}
	if len(body.Refs) != 0 {
		t.Errorf("refs 应为空（资产 id 与具体库绑定，不能写死），实际 %d 项", len(body.Refs))
	}
	// @1 是这个模板唯一的占位符：用户补上自己的照片后即指向第 1 张参考图。
	if !strings.Contains(body.Prompt, "@1") {
		t.Error("提示词应含 @1 占位")
	}
}

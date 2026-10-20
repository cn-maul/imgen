package store

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/imgen/imgen/internal/domain"
)

func TestPresetCRUD(t *testing.T) {
	s := openTemp(t)

	created, err := s.CreatePreset(PromptPreset{
		Name:     "  人像换背景  ",
		Category: "换背景",
		Body:     json.RawMessage(`{"prompt":"把 @1 的背景换成海边"}`),
	})
	if err != nil {
		t.Fatalf("CreatePreset: %v", err)
	}
	if created.ID == "" {
		t.Fatal("自动生成的 id 为空")
	}
	if created.Name != "人像换背景" {
		t.Errorf("名称未去空白: %q", created.Name)
	}

	got, err := s.GetPreset(created.ID)
	if err != nil {
		t.Fatalf("GetPreset: %v", err)
	}
	if got.Category != "换背景" || string(got.Body) != `{"prompt":"把 @1 的背景换成海边"}` {
		t.Errorf("读回内容不符: %+v", got)
	}

	updated, err := s.UpdatePreset(created.ID, PromptPreset{
		Name: "人像换背景 v2",
		Body: json.RawMessage(`{"prompt":"把 @2 的背景换成海边"}`),
	})
	if err != nil {
		t.Fatalf("UpdatePreset: %v", err)
	}
	if updated.Name != "人像换背景 v2" {
		t.Errorf("更新后名称 = %q", updated.Name)
	}
	if updated.UpdatedAt == created.UpdatedAt {
		t.Error("updated_at 未刷新")
	}

	list, err := s.ListPresets()
	if err != nil {
		t.Fatalf("ListPresets: %v", err)
	}
	// 库里还有迁移 0004 播种的内置预设，故长度为 2。
	if len(list) != 2 {
		t.Fatalf("列表长度 = %d, want 2（内置 1 + 自建 1）", len(list))
	}

	if err := s.DeletePreset(created.ID); err != nil {
		t.Fatalf("DeletePreset: %v", err)
	}
	if _, err := s.GetPreset(created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("删除后应 NotFound, got %v", err)
	}
}

func TestPresetNotFound(t *testing.T) {
	s := openTemp(t)
	if _, err := s.GetPreset("nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetPreset 未知 id 应 NotFound, got %v", err)
	}
	if err := s.DeletePreset("nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("DeletePreset 未知 id 应 NotFound, got %v", err)
	}
	if _, err := s.UpdatePreset("nope", PromptPreset{Name: "x"}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdatePreset 未知 id 应 NotFound, got %v", err)
	}
}

// TestPresetBodyDefaultsToEmptyObject 保证缺省 body 落库为 {} 而非空字节，
// 否则前端 JSON.parse 会拿到空串。
func TestPresetBodyDefaultsToEmptyObject(t *testing.T) {
	s := openTemp(t)
	p, err := s.CreatePreset(PromptPreset{Name: "空 body"})
	if err != nil {
		t.Fatalf("CreatePreset: %v", err)
	}
	if string(p.Body) != `{}` {
		t.Errorf("body = %q, want {}", string(p.Body))
	}
}

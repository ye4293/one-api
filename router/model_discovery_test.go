package router

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/controller"
	"github.com/songquanpeng/one-api/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupModelDiscovery(t *testing.T) (*gorm.DB, *gin.Engine) {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	oldDB, oldRedis := model.DB, common.RedisEnabled
	model.DB, common.RedisEnabled = db, false
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = oldDB, oldRedis
		_ = sqlDB.Close()
	})
	if err := db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}); err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"Lv1", "Lv2"} {
		user := model.User{Username: group, Group: group, AccessToken: group, AffCode: group, Status: common.UserStatusEnabled}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		token := model.Token{UserId: user.Id, Key: "discovery" + group, Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
		if err := db.Create(&token).Error; err != nil {
			t.Fatal(err)
		}
	}
	mapping := `{"public-alias":"private-upstream-model"}`
	channels := []model.Channel{
		{Id: 1, Status: common.ChannelStatusEnabled, Group: "Lv1", Models: "gemini-3.6-pro,gemini-3.7-pro,gemini-3.8-pro,argon-preview,public-alias,gpt-4", ModelMapping: &mapping},
		{Id: 2, Status: common.ChannelStatusEnabled, Group: "Lv1", Models: "argon-preview"},
		{Id: 3, Status: common.ChannelStatusEnabled, Group: "Lv2", Models: "private-chat"},
		{Id: 4, Status: common.ChannelStatusManuallyDisabled, Group: "Lv1", Models: "disabled-channel"},
		{Id: 5, Status: common.ChannelStatusAutoDisabled, Group: "Lv1", Models: "auto-disabled-channel"},
	}
	for i := range channels {
		if err := db.Create(&channels[i]).Error; err != nil {
			t.Fatal(err)
		}
		if err := channels[i].AddAbilities(); err != nil {
			t.Fatal(err)
		}
	}
	// 模拟状态同步不一致：渠道已禁用，但能力仍为 enabled。
	if err := db.Model(&model.Ability{}).Where("channel_id IN ?", []int{4, 5}).Update("enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	abilities := []model.Ability{
		{Group: "Lv1", Model: "ability-disabled", ChannelId: 1, Enabled: false},
		{Group: "Lv1", Model: "auto-disabled-model", ChannelId: 1, Enabled: true, AutoDisabled: true},
		{Group: "Lv1", Model: "orphan-model", ChannelId: 999, Enabled: true},
		{Group: "Lv1", Model: "", ChannelId: 1, Enabled: true},
		{Group: "Lv1", Model: "   ", ChannelId: 1, Enabled: true},
	}
	if err := db.Create(&abilities).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	SetRelayRouter(r)
	return db, r
}

func requestModelDiscovery(r http.Handler, path, group string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if group != "" {
		req.Header.Set("Authorization", "Bearer sk-discovery"+group)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func assertDiscoveredModels(t *testing.T, w *httptest.ResponseRecorder, want []string) []controller.OpenAIModels {
	t.Helper()
	var response struct {
		Object string                    `json:"object"`
		Data   []controller.OpenAIModels `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Object != "list" || response.Data == nil {
		t.Fatalf("模型列表响应异常: status=%d body=%.500s", w.Code, w.Body.String())
	}
	got := make([]string, 0, len(response.Data))
	for _, item := range response.Data {
		got = append(got, item.Id)
		if item.Object != "model" || item.Root != item.Id || item.OwnedBy == "" {
			t.Fatalf("模型元数据异常: %+v", item)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("返回模型=%v, 期望=%v", got, want)
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("用户模型列表不能被共享缓存")
	}
	return response.Data
}

func TestModelDiscoveryUsesConfiguredModels(t *testing.T) {
	_, r := setupModelDiscovery(t)
	want := []string{"argon-preview", "gemini-3.6-pro", "gemini-3.7-pro", "gemini-3.8-pro", "gpt-4", "public-alias"}
	// 请求参数不能改变鉴权用户所属分组。
	items := assertDiscoveredModels(t, requestModelDiscovery(r, "/v1/models?group=Lv2", "Lv1"), want)
	assertDiscoveredModels(t, requestModelDiscovery(r, "/v1/models", "Lv2"), []string{"private-chat"})
	for _, item := range items {
		w := requestModelDiscovery(r, "/v1/models/"+item.Id, "Lv1")
		var detail controller.OpenAIModels
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &detail) != nil || !reflect.DeepEqual(detail, item) {
			t.Fatalf("列表与详情不一致: model=%s status=%d body=%.500s", item.Id, w.Code, w.Body.String())
		}
		if item.Id == "gpt-4" && len(item.Permission) == 0 {
			t.Fatal("已有内置模型元数据应保留")
		}
	}
	for _, id := range []string{"private-chat", "private-upstream-model", "disabled-channel", "auto-disabled-channel", "auto-disabled-model", "ability-disabled", "orphan-model", "gpt-3.5-turbo"} {
		w := requestModelDiscovery(r, "/v1/models/"+id, "Lv1")
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error.Code != "model_not_found" {
			t.Fatalf("不可用模型不应暴露: model=%s body=%.500s", id, w.Body.String())
		}
	}
}

func TestModelDiscoveryReflectsChannelChanges(t *testing.T) {
	db, r := setupModelDiscovery(t)
	// 空分组返回 []；能力 enabled 的旧值不能让禁用渠道继续出现在列表中。
	if err := db.Model(&model.Channel{}).Where("id IN ?", []int{1, 2}).Update("status", common.ChannelStatusManuallyDisabled).Error; err != nil {
		t.Fatal(err)
	}
	assertDiscoveredModels(t, requestModelDiscovery(r, "/v1/models", "Lv1"), []string{})
	channel := model.Channel{Status: common.ChannelStatusEnabled, Group: "Lv1", Models: "argon-new-release"}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	if err := channel.AddAbilities(); err != nil {
		t.Fatal(err)
	}
	assertDiscoveredModels(t, requestModelDiscovery(r, "/v1/models", "Lv1"), []string{"argon-new-release"})
	channel.Models = "gemini-future-release"
	if err := db.Model(&channel).Update("models", channel.Models).Error; err != nil {
		t.Fatal(err)
	}
	if err := channel.UpdateAbilities(); err != nil {
		t.Fatal(err)
	}
	assertDiscoveredModels(t, requestModelDiscovery(r, "/v1/models", "Lv1"), []string{"gemini-future-release"})
	if err := db.Model(&model.User{}).Where("username = ?", "Lv1").Update("group", "Lv2").Error; err != nil {
		t.Fatal(err)
	}
	assertDiscoveredModels(t, requestModelDiscovery(r, "/v1/models", "Lv1"), []string{"private-chat"})
}

func TestModelDiscoveryAuthenticationAndQueryFailure(t *testing.T) {
	db, r := setupModelDiscovery(t)
	for _, path := range []string{"/v1/models", "/v1/models/argon-preview"} {
		for _, group := range []string{"", "invalid"} {
			if w := requestModelDiscovery(r, path, group); w.Code != http.StatusUnauthorized {
				t.Fatalf("无效鉴权应返回 401: path=%s status=%d", path, w.Code)
			}
		}
	}
	for _, failure := range []string{"abilities", "group"} {
		t.Run(failure, func(t *testing.T) {
			callback := "test:model_discovery_failure"
			if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if (failure == "abilities" && tx.Statement.Table == "abilities") ||
					(failure == "group" && tx.Statement.Table == "users" && strings.Contains(strings.Join(tx.Statement.Selects, ","), "group")) {
					tx.AddError(errors.New("private database diagnostic"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
			for _, path := range []string{"/v1/models", "/v1/models/argon-preview"} {
				w := requestModelDiscovery(r, path, "Lv1")
				var body struct {
					Error struct{ Code string } `json:"error"`
				}
				if w.Code != http.StatusInternalServerError || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error.Code != "model_list_unavailable" {
					t.Fatalf("查询失败不应回退静态目录: status=%d body=%.500s", w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "private database diagnostic") {
					t.Fatal("不应在响应中暴露数据库错误")
				}
			}
		})
	}
}

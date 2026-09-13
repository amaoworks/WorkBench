package modules

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"

	"workbench/internal/contracts"
)

const reservedModuleID = "core"

var allowedCapabilities = map[string]struct{}{
	"lifecycle": {},
	"pages":     {},
	"settings":  {},
}

func ParseBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return "", &Error{Status: 400, Code: "invalid_connection", Message: "baseUrl 必须是 HTTP 或 HTTPS 来源"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", &Error{Status: 400, Code: "invalid_connection", Message: "baseUrl 必须是 HTTP 或 HTTPS 来源"}
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", &Error{Status: 400, Code: "invalid_connection", Message: "baseUrl 不能包含账号、路径、查询参数或片段"}
	}
	trimmedPath := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if trimmedPath != "" {
		return "", &Error{Status: 400, Code: "invalid_connection", Message: "baseUrl 不能包含账号、路径、查询参数或片段"}
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func hostnameOfOrigin(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	host, _, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return parsed.Hostname()
	}
	return host
}

func ValidateExternalManifest(raw []byte, expected contracts.ModuleID) (contracts.ExternalManifest, error) {
	var manifest contracts.ExternalManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return contracts.ExternalManifest{}, &Error{Status: 400, Code: "invalid_manifest", Message: "模块描述不是有效 JSON"}
	}
	if !moduleIDPattern.MatchString(string(manifest.ID)) || manifest.ID == reservedModuleID {
		return contracts.ExternalManifest{}, &Error{Status: 400, Code: "invalid_manifest", Message: "模块 ID 无效"}
	}
	if expected != "" && manifest.ID != expected {
		return contracts.ExternalManifest{}, &Error{Status: 400, Code: "module_id_mismatch", Message: "模块 ID 与已接入记录不一致"}
	}
	if strings.TrimSpace(manifest.Name) == "" || strings.TrimSpace(manifest.Version) == "" {
		return contracts.ExternalManifest{}, &Error{Status: 400, Code: "invalid_manifest", Message: "模块名称和版本不能为空"}
	}
	if manifest.ProtocolVersion != contracts.ExternalProtocolVersion {
		return contracts.ExternalManifest{}, &Error{Status: 400, Code: "unsupported_protocol", Message: fmt.Sprintf("不支持的协议版本 %d", manifest.ProtocolVersion)}
	}
	hasLifecycle := false
	hasPages := false
	hasSettings := false
	seenCap := make(map[string]struct{}, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		if _, ok := allowedCapabilities[capability]; !ok {
			return contracts.ExternalManifest{}, &Error{Status: 400, Code: "unsupported_capability", Message: "不支持的能力: " + capability}
		}
		if _, dup := seenCap[capability]; dup {
			return contracts.ExternalManifest{}, &Error{Status: 400, Code: "invalid_manifest", Message: "能力声明重复"}
		}
		seenCap[capability] = struct{}{}
		switch capability {
		case "lifecycle":
			hasLifecycle = true
		case "pages":
			hasPages = true
		case "settings":
			hasSettings = true
		}
	}
	if !hasLifecycle {
		return contracts.ExternalManifest{}, &Error{Status: 400, Code: "invalid_manifest", Message: "必须声明 lifecycle 能力"}
	}
	if err := validatePages(manifest.ID, manifest.Pages, hasPages); err != nil {
		return contracts.ExternalManifest{}, err
	}
	if err := validateSettings(manifest.Settings, hasSettings); err != nil {
		return contracts.ExternalManifest{}, err
	}
	if manifest.Icon == "" {
		manifest.Icon = "module.default"
	}
	return manifest, nil
}

func validatePages(id contracts.ModuleID, pages []contracts.ExternalPage, declared bool) error {
	if declared && len(pages) == 0 {
		return &Error{Status: 400, Code: "invalid_manifest", Message: "声明 pages 后必须提供页面入口"}
	}
	seen := make(map[string]struct{}, len(pages))
	prefix := string(id) + "."
	for _, page := range pages {
		if page.Label == "" || !strings.HasPrefix(page.Key, prefix) || page.Key == prefix {
			return &Error{Status: 400, Code: "invalid_manifest", Message: "页面 key 必须归属模块且包含名称"}
		}
		name := strings.TrimPrefix(page.Key, prefix)
		if !moduleIDPattern.MatchString(name) {
			return &Error{Status: 400, Code: "invalid_manifest", Message: "页面名称无效"}
		}
		if _, exists := seen[page.Key]; exists {
			return &Error{Status: 400, Code: "invalid_manifest", Message: "页面 key 重复"}
		}
		seen[page.Key] = struct{}{}
		if err := validateEntry(page.Entry, "/ui/"); err != nil {
			return err
		}
	}
	return nil
}

func validateSettings(settings *contracts.ExternalSettingsEntry, declared bool) error {
	if declared && (settings == nil || settings.Entry == "") {
		return &Error{Status: 400, Code: "invalid_manifest", Message: "声明 settings 后必须提供设置入口"}
	}
	if settings == nil {
		return nil
	}
	return validateEntry(settings.Entry, "/settings/")
}

func validateEntry(entry, prefix string) error {
	entry = strings.TrimSpace(entry)
	parsed, err := url.Parse(entry)
	if err != nil || parsed.IsAbs() || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || parsed.Opaque != "" {
		return &Error{Status: 400, Code: "invalid_manifest", Message: "页面入口必须是站点内相对路径"}
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(parsed.Path, "/"))
	if !strings.HasPrefix(cleaned, prefix) || cleaned == strings.TrimSuffix(prefix, "/") || cleaned == prefix {
		return &Error{Status: 400, Code: "invalid_manifest", Message: "页面入口必须位于约定目录内"}
	}
	if strings.Contains(cleaned, "/_workbench") {
		return &Error{Status: 400, Code: "invalid_manifest", Message: "页面入口不能指向管理路径"}
	}
	return nil
}

func navigationFromManifest(manifest contracts.ExternalManifest) []contracts.NavigationItem {
	items := make([]contracts.NavigationItem, 0, len(manifest.Pages))
	prefix := string(manifest.ID) + "."
	for _, page := range manifest.Pages {
		name := strings.TrimPrefix(page.Key, prefix)
		items = append(items, contracts.NavigationItem{
			Label:   page.Label,
			Route:   "/apps/" + string(manifest.ID) + "/" + name,
			PageKey: page.Key,
			Order:   page.Order,
		})
	}
	return items
}

func listedPages(manifest contracts.ExternalManifest) []ListedPage {
	pages := make([]ListedPage, 0, len(manifest.Pages))
	for _, page := range manifest.Pages {
		pages = append(pages, ListedPage{Key: page.Key, Label: page.Label, Entry: page.Entry, Order: page.Order})
	}
	return pages
}

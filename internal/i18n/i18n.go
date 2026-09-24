// Package i18n provides the panel's built-in translations and language selection.
package i18n

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const CookieName = "panel4wp_language"

type Language struct{ Code, Name, Dir string }

var languages = []Language{{"en", "English", "ltr"}, {"ar", "العربية", "rtl"}, {"fa", "فارسی", "rtl"}, {"es", "Español", "ltr"}, {"de", "Deutsch", "ltr"}, {"fr", "Français", "ltr"}, {"zh", "中文", "ltr"}, {"ja", "日本語", "ltr"}}

// Languages returns a copy so callers cannot modify the supported language list.
func Languages() []Language { return append([]Language(nil), languages...) }

// Normalize accepts supported language tags, including regional variants.
// Unsupported or malformed tags return an empty string.
func Normalize(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	parts := strings.Split(tag, "-")
	for _, part := range parts {
		if part == "" {
			return ""
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				return ""
			}
		}
	}
	for _, language := range languages {
		if parts[0] == language.Code {
			return language.Code
		}
	}
	return ""
}

// Detect respects quality weights and original preference order. Unsupported,
// malformed and explicitly excluded (q=0) entries do not select a language.
func Detect(header string) string {
	type preference struct {
		language string
		quality  float64
	}
	var candidates []preference
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(item, ";")
		lang := Normalize(parts[0])
		if lang == "" {
			continue
		}
		quality := 1.0
		valid := true
		seenQ := false
		for _, parameter := range parts[1:] {
			pair := strings.SplitN(strings.TrimSpace(parameter), "=", 2)
			if len(pair) != 2 || !strings.EqualFold(pair[0], "q") || seenQ {
				valid = false
				break
			}
			seenQ = true
			value := strings.TrimSpace(pair[1])
			if value == "" || strings.ContainsAny(value, "eE+-") {
				valid = false
				break
			}
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil || parsed < 0 || parsed > 1 {
				valid = false
				break
			}
			quality = parsed
		}
		if valid && quality > 0 {
			candidates = append(candidates, preference{lang, quality})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].quality > candidates[j].quality })
	if len(candidates) > 0 {
		return candidates[0].language
	}
	return "en"
}

// Resolve prefers a saved valid choice over the browser's language preferences.
func Resolve(r *http.Request) string {
	if cookie, err := r.Cookie(CookieName); err == nil {
		if lang := Normalize(cookie.Value); lang != "" {
			return lang
		}
	}
	return Detect(r.Header.Get("Accept-Language"))
}

// SetCookie saves an explicit choice for one year. Invalid choices are rejected.
func SetCookie(w http.ResponseWriter, language string, secure bool) bool {
	lang := Normalize(language)
	if lang == "" {
		return false
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: lang, Path: "/", MaxAge: 365 * 24 * 60 * 60, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	return true
}

func Direction(language string) string {
	lang := Normalize(language)
	if lang == "ar" || lang == "fa" {
		return "rtl"
	}
	return "ltr"
}

var aliases = map[string]string{
	"welcome_back": "login_title", "administrator_password": "admin_password",
	"log_in": "login", "log_out": "logout", "built_for_hosting": "footer",
	"github_star_title": "star_github", "github_star_help": "github_help", "whats_next": "roadmap",
	"create_site_hint": "create_description", "wordpress_admin_email": "admin_email",
	"plan_small": "small", "plan_standard": "standard", "plan_large": "large",
	"your_sites": "my_sites", "first_site_title": "empty_sites_title", "first_site_help": "empty_sites_guide",
	"create_first_site": "first_site", "live_resource_usage": "live_usage",
	"status_running": "running", "status_stopped": "stopped", "status_failed": "failed",
	"status_creating": "creating", "status_deleting": "deleting",
	"backups_recovery": "backups_title", "restore_warning": "backups_guide", "restore_backup": "restore",
	"type_domain_restore": "confirm_domain", "type_domain_delete": "confirm_domain", "type_domain_confirm": "confirm_domain",
	"open_database_manager": "open_database", "database_access_hint": "database_guide",
	"smtp_site_hint": "site_mail_hint", "enable_global_smtp": "enable_smtp",
	"delete_site_warning": "delete_warning", "delete_site_data": "delete_site",
	"mail_server_settings": "settings_title", "smtp_global_hint": "settings_guide",
	"your_company": "sender_name", "password_encrypted_hint": "password_protected", "save_mail_settings": "save_mail",
	"recent_activity": "activity_title", "on_horizon": "roadmap_title", "roadmap_hint": "roadmap_guide",
	"sftp_access": "sftp", "secure_file_transfer": "sftp_hint", "automated_alerts": "alerts",
	"server_health": "alerts_hint", "container_versions": "image_upgrades_hint", "storage_quota_hint": "storage_quotas_hint",
	"scheduled_remote_backups": "remote_backups", "protect_off_server": "remote_backups_hint", "customer_workspaces": "customer_accounts_hint",
	"file_manager": "files_title", "back_to_site": "back_to_sites", "directory_empty": "empty_directory",
	"upload_max": "upload", "file_access_hint": "files_guide",
}

// T falls back to English for unsupported languages or missing translations.
// Unknown keys are returned unchanged to keep failures visible during development.
func T(language, key string) string {
	if alias := aliases[key]; alias != "" {
		key = alias
	}
	if translated := dictionaries[Normalize(language)][key]; translated != "" {
		return translated
	}
	if translated := dictionaries["en"][key]; translated != "" {
		return translated
	}
	return key
}

// Keys returns the supported translation keys in deterministic order.
func Keys() []string {
	keys := make([]string, 0, len(dictionaries["en"]))
	for key := range dictionaries["en"] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

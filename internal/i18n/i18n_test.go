package i18n

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectAndResolveLanguage(t *testing.T) {
	for _, test := range []struct{ header, want string }{
		{"fa-IR,fa;q=0.9,en;q=0.8", "fa"},
		{"es;q=0.4,de;q=0.9", "de"},
		{"ru,xx;q=0.5", "en"},
		{"zh-CN;q=0,ja;q=0.8", "ja"},
	} {
		if got := Detect(test.header); got != test.want {
			t.Errorf("Detect(%q)=%q want %q", test.header, got, test.want)
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Language", "de")
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "ar"})
	if got := Resolve(r); got != "ar" {
		t.Fatalf("cookie did not win: %q", got)
	}
}

func TestLanguagesAreCompleteAndDirectional(t *testing.T) {
	if len(Languages()) != 8 {
		t.Fatalf("got %d languages", len(Languages()))
	}
	keys := Keys()
	for _, language := range Languages() {
		for _, key := range keys {
			if dictionaries[language.Code][key] == "" {
				t.Errorf("%s missing %s", language.Code, key)
			}
		}
	}
	if Direction("fa-IR") != "rtl" || Direction("ar") != "rtl" || Direction("ja") != "ltr" {
		t.Fatal("wrong direction")
	}
}

func TestAliasTargetsExist(t *testing.T) {
	for alias, target := range aliases {
		if dictionaries["en"][target] == "" {
			t.Errorf("alias %s points to missing key %s", alias, target)
		}
	}
}

func TestSetCookieRejectsUnsupportedLanguage(t *testing.T) {
	w := httptest.NewRecorder()
	if SetCookie(w, "ru", false) {
		t.Fatal("unsupported language accepted")
	}
	if !SetCookie(w, "fr-FR", true) {
		t.Fatal("supported language rejected")
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Value != "fr" || cookie.Path != "/" || !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("bad cookie: %#v", cookie)
	}
}

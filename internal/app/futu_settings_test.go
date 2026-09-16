package app

import "testing"

func TestFutuSettingsBelongToInvestment(t *testing.T) {
	a := newTestApp(t)
	token, cookies := csrf(t, a.Handler())
	if got := request(t, a.Handler(), "GET", "/api/settings/futu", nil, "", cookies); got.Code != 404 {
		t.Fatal("global Futu settings route remains")
	}
	body := []byte(`{"enabled":false,"account":"fixture","password":"fixture password"}`)
	if got := request(t, a.Handler(), "PUT", "/api/modules/investment/futu", body, "", cookies); got.Code != 400 {
		t.Fatal("Futu settings bypassed CSRF")
	}
	if got := request(t, a.Handler(), "PUT", "/api/modules/investment/futu", body, token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if got := request(t, a.Handler(), "PUT", "/api/modules/investment/enabled", []byte(`{"enabled":false}`), token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	for _, path := range []string{"/api/modules/investment/futu", "/api/modules/investment/overnight"} {
		if got := request(t, a.Handler(), "GET", path, nil, "", cookies); got.Code != 503 {
			t.Fatal("disabled investment exposed business settings", path)
		}
	}
}

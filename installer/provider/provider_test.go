package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func server(t *testing.T, h http.HandlerFunc) *httptest.Server {
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestZonesListsAccountsAndSendsTheTokenAsABearerHeaderOnly(t *testing.T) {
	var auth, rawURL string
	s := server(t, func(w http.ResponseWriter, r *http.Request) {
		auth, rawURL = r.Header.Get("Authorization"), r.URL.String()
		fmt.Fprint(w, `{"success":true,"result_info":{"total_pages":1},"result":[
		 {"name":"B.example","account":{"id":"a2","name":"Two"}},
		 {"name":"a.example","account":{"id":"a1","name":""}},
		 {"name":"no-account.example","account":{}}]}`)
	})
	zones, err := Client{CloudflareBase: s.URL}.Zones(context.Background(), "secret-token")
	if err != nil || len(zones) != 2 {
		t.Fatalf("%v %v", zones, err)
	}
	if zones[0].Name != "a.example" || zones[0].Label() != "a.example — account: Unnamed Cloudflare account" {
		t.Fatalf("%+v", zones[0])
	}
	if auth != "Bearer secret-token" || strings.Contains(rawURL, "secret-token") {
		t.Fatalf("auth=%q url=%q", auth, rawURL)
	}
}

func TestZonesFollowsPagination(t *testing.T) {
	s := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			fmt.Fprint(w, `{"success":true,"result_info":{"total_pages":2},"result":[{"name":"one.example","account":{"id":"a"}}]}`)
			return
		}
		fmt.Fprint(w, `{"success":true,"result_info":{"total_pages":2},"result":[{"name":"two.example","account":{"id":"a"}}]}`)
	})
	zones, err := Client{CloudflareBase: s.URL}.Zones(context.Background(), "t")
	if err != nil || len(zones) != 2 {
		t.Fatalf("%v %v", zones, err)
	}
}

func TestARejectedCloudflareTokenIsReportedAsRejected(t *testing.T) {
	s := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"success":false}`)
	})
	_, err := Client{CloudflareBase: s.URL}.Zones(context.Background(), "t")
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("got %v", err)
	}
}

func TestATransportErrorNeverQuotesTheToken(t *testing.T) {
	_, err := Client{CloudflareBase: "http://127.0.0.1:1"}.Zones(context.Background(), "super-secret")
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestTailnetsAreDerivedFromDeviceDNSNames(t *testing.T) {
	s := server(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"devices":[{"name":"host1.tail1234.ts.net."},{"name":"host2.tail1234.ts.net"},{"name":"x.other.ts.net"},{"name":"nodots"}]}`)
	})
	got, err := Client{TailscaleBase: s.URL}.Tailnets(context.Background(), "t")
	if err != nil || strings.Join(got, ",") != "other.ts.net,tail1234.ts.net" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestAnEmptyTailnetIsNotAnError(t *testing.T) {
	s := server(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"devices":[]}`) })
	got, err := Client{TailscaleBase: s.URL}.Tailnets(context.Background(), "t")
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestEmptyTailnetUsesMagicDNSSearchDomain(t *testing.T) {
	s := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tailnet/-/devices":
			fmt.Fprint(w, `{"devices":[]}`)
		case "/tailnet/-/dns/preferences":
			fmt.Fprint(w, `{"magicDNS":true}`)
		case "/tailnet/-/dns/searchpaths":
			fmt.Fprint(w, `{"searchPaths":["Tail1234.ts.net.","example.com"]}`)
		default:
			http.NotFound(w, r)
		}
	})
	got, err := Client{TailscaleBase: s.URL}.Tailnets(context.Background(), "t")
	if err != nil || strings.Join(got, ",") != "tail1234.ts.net" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestEmptyTailnetDoesNotTreatCustomSearchDomainAsTailnet(t *testing.T) {
	s := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tailnet/-/devices":
			fmt.Fprint(w, `{"devices":[]}`)
		case "/tailnet/-/dns/preferences":
			fmt.Fprint(w, `{"magicDNS":false}`)
		case "/tailnet/-/dns/searchpaths":
			t.Error("should not use search paths with MagicDNS disabled")
		default:
			http.NotFound(w, r)
		}
	})
	got, err := Client{TailscaleBase: s.URL}.Tailnets(context.Background(), "t")
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestTailnetHTTPSReadsTheSettingAndNeverTreatsUnknownAsOff(t *testing.T) {
	for name, tc := range map[string]struct {
		body      string
		on, known bool
	}{
		"on":      {`{"httpsEnabled":true}`, true, true},
		"off":     {`{"httpsEnabled":false}`, false, true},
		"unknown": {`{"devicesApprovalOn":true}`, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			var path, auth string
			s := server(t, func(w http.ResponseWriter, r *http.Request) {
				path, auth = r.URL.Path, r.Header.Get("Authorization")
				fmt.Fprint(w, tc.body)
			})
			on, known, err := Client{TailscaleBase: s.URL}.TailnetHTTPS(context.Background(), "tok")
			if err != nil || on != tc.on || known != tc.known {
				t.Fatalf("on=%v known=%v err=%v", on, known, err)
			}
			if path != "/tailnet/-/settings" || auth != "Bearer tok" {
				t.Fatalf("path=%q auth=%q", path, auth)
			}
		})
	}
}

func TestEnableTailnetHTTPSWritesThenReadsBackAndExplainsARefusal(t *testing.T) {
	enabled := false
	s := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			enabled = true
			return
		}
		fmt.Fprintf(w, `{"httpsEnabled":%v}`, enabled)
	})
	if err := (Client{TailscaleBase: s.URL}).EnableTailnetHTTPS(context.Background(), "tok"); err != nil || !enabled {
		t.Fatalf("enabled=%v err=%v", enabled, err)
	}

	refused := server(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	if err := (Client{TailscaleBase: refused.URL}).EnableTailnetHTTPS(context.Background(), "tok"); !errors.Is(err, ErrRejected) {
		t.Fatalf("a refusal was not reported as rejected: %v", err)
	}

	liar := server(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"httpsEnabled":false}`) })
	if err := (Client{TailscaleBase: liar.URL}).EnableTailnetHTTPS(context.Background(), "tok"); err == nil {
		t.Fatal("success was reported although the setting still reads off")
	}
}

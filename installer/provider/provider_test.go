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
		fmt.Fprint(w, `{"devices":[{"dnsName":"host1.tail1234.ts.net."},{"dnsName":"host2.tail1234.ts.net"},{"dnsName":"x.other.ts.net"},{"dnsName":"nodots"}]}`)
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

package inspect

import (
	"reflect"
	"testing"
)

func TestLinkTopologyResolvesPodsAndSettings(t *testing.T) {
	pods, err := parsePods([]byte(`{"items":[
	 {"metadata":{"name":"web-1","namespace":"apps","labels":{"app":"web"}},"spec":{"containers":[{"env":[{"name":"DB","value":"postgres://db:5432/x"},{"name":"AUTH","value":"https://sso.identity.svc.cluster.local/api"},{"name":"SELF","value":"http://web:80"}]}]}},
	 {"metadata":{"name":"db-0","namespace":"apps","labels":{"app":"db"}},"spec":{"containers":[{}]}},
	 {"metadata":{"name":"sso-1","namespace":"identity","labels":{"app":"sso"}},"spec":{"containers":[{}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	services, err := parseServices([]byte(`{"items":[
	 {"metadata":{"name":"web","namespace":"apps"},"spec":{"type":"ClusterIP","selector":{"app":"web"},"ports":[{"port":80}]}},
	 {"metadata":{"name":"db","namespace":"apps"},"spec":{"selector":{"app":"db"},"ports":[{"port":5432,"protocol":"TCP"}]}},
	 {"metadata":{"name":"sso","namespace":"identity"},"spec":{"selector":{"app":"sso"}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	rke2 := &RKE2{Pods: pods, KubeServices: services}
	linkTopology(rke2)

	if got := rke2.KubeServices[0].Pods; !reflect.DeepEqual(got, []string{"web-1"}) {
		t.Errorf("web service pods = %v", got)
	}
	if got := rke2.KubeServices[0].Ports; !reflect.DeepEqual(got, []string{"80/TCP"}) {
		t.Errorf("ports = %v", got)
	}
	// A bare name finds a service in the same namespace, a qualified name finds one in
	// another, and a pod's own service is never reported as something it calls.
	if got := rke2.Pods[0].Calls; !reflect.DeepEqual(got, []string{"apps/db", "identity/sso"}) {
		t.Errorf("web-1 calls = %v", got)
	}
	if len(rke2.Pods[1].Calls) != 0 {
		t.Errorf("db-0 should call nothing, got %v", rke2.Pods[1].Calls)
	}
}

func TestParseIngressesKeepsRoutes(t *testing.T) {
	ingresses, err := parseIngresses([]byte(`{"items":[{"metadata":{"name":"i","namespace":"apps"},"spec":{"rules":[{"host":"a.example","http":{"paths":[{"path":"/","backend":{"service":{"name":"web"}}}]}}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Route{{Host: "a.example", Path: "/", Service: "web"}}
	if !reflect.DeepEqual(ingresses[0].Routes, want) {
		t.Errorf("routes = %v", ingresses[0].Routes)
	}
}

func TestParseIngressesKeepsDefaultBackend(t *testing.T) {
	ingresses, err := parseIngresses([]byte(`{"items":[{"metadata":{"name":"plex-tailnet","namespace":"media"},"spec":{"ingressClassName":"tailscale","defaultBackend":{"service":{"name":"plex"}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []Route{{Service: "plex"}}
	if !reflect.DeepEqual(ingresses[0].Routes, want) {
		t.Errorf("routes = %v", ingresses[0].Routes)
	}
}

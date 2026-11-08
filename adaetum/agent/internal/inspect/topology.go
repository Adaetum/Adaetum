package inspect

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Service is a Kubernetes Service and the pods it currently sends traffic to. The app
// page draws "who reaches it, and what it reaches" from these, so the pod list is
// resolved here (a Service selects pods by label, and labels are not in the report).
type Service struct {
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Type      string   `json:"type,omitempty"`
	Ports     []string `json:"ports,omitempty"`
	// Pods are the names of the pods the service's selector matches. Empty for a service
	// with no selector (external names, hand-made endpoints) or with nothing behind it.
	Pods []string `json:"pods,omitempty"`

	// selector is only needed to resolve Pods.
	selector map[string]string
}

// Route is one rule of an Ingress: a host and path sent to a service.
type Route struct {
	Host    string `json:"host,omitempty"`
	Path    string `json:"path,omitempty"`
	Service string `json:"service"`
}

// parseServices reads `kubectl get services -A -o json`.
func parseServices(data []byte) ([]Service, error) {
	var list struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				Type     string            `json:"type"`
				Selector map[string]string `json:"selector"`
				Ports    []struct {
					Port     int    `json:"port"`
					Protocol string `json:"protocol"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	services := make([]Service, 0, len(list.Items))
	for _, item := range list.Items {
		service := Service{Namespace: item.Metadata.Namespace, Name: item.Metadata.Name, Type: item.Spec.Type, selector: item.Spec.Selector}
		for _, port := range item.Spec.Ports {
			protocol := port.Protocol
			if protocol == "" {
				protocol = "TCP"
			}
			service.Ports = append(service.Ports, strconv.Itoa(port.Port)+"/"+protocol)
		}
		services = append(services, service)
	}
	return services, nil
}

// wordPattern finds hostname-like words in a setting's value ("http://sabnzbd:8080/api"
// yields http, sabnzbd, 8080).
var wordPattern = regexp.MustCompile(`[a-z0-9]+(?:[-.][a-z0-9]+)*`)

// linkTopology resolves, after the pod and service queries have both finished:
//   - each service's pods, by label selector;
//   - each pod's Calls, the services its own settings (environment values, arguments)
//     name. That is a hint, not a traffic record: a connection made from a mounted
//     config file or a secret is invisible here, so the page labels it "found in settings".
//
// A pod never "calls" a service that sends traffic to itself.
func linkTopology(rke2 *RKE2) {
	if len(rke2.KubeServices) == 0 {
		return
	}
	podsByNamespace := map[string][]int{}
	for index, pod := range rke2.Pods {
		podsByNamespace[pod.Namespace] = append(podsByNamespace[pod.Namespace], index)
	}

	// service key "namespace/name" -> pods behind it.
	behind := map[string]map[string]bool{}
	for index := range rke2.KubeServices {
		service := &rke2.KubeServices[index]
		if len(service.selector) == 0 {
			continue
		}
		key := service.Namespace + "/" + service.Name
		behind[key] = map[string]bool{}
		for _, podIndex := range podsByNamespace[service.Namespace] {
			pod := rke2.Pods[podIndex]
			if matchesSelector(pod.labels, service.selector) {
				service.Pods = append(service.Pods, pod.Name)
				behind[key][pod.Name] = true
			}
		}
		sort.Strings(service.Pods)
	}

	for podIndex := range rke2.Pods {
		pod := &rke2.Pods[podIndex]
		if len(pod.settings) == 0 {
			continue
		}
		seen := map[string]bool{}
		for _, text := range pod.settings {
			for _, word := range wordPattern.FindAllString(strings.ToLower(text), -1) {
				for _, service := range rke2.KubeServices {
					if !mentions(word, service, pod.Namespace) {
						continue
					}
					key := service.Namespace + "/" + service.Name
					if behind[key][pod.Name] || seen[key] {
						continue
					}
					seen[key] = true
				}
			}
		}
		for key := range seen {
			pod.Calls = append(pod.Calls, key)
		}
		sort.Strings(pod.Calls)
	}
}

// mentions says whether a word from a setting names the service: its bare name when it
// lives in the pod's own namespace (how most in-namespace settings are written), or
// "name.namespace" with anything after it (name.namespace.svc.cluster.local) from anywhere.
func mentions(word string, service Service, fromNamespace string) bool {
	if service.Namespace == fromNamespace && word == service.Name {
		return true
	}
	prefix := service.Name + "." + service.Namespace
	return word == prefix || strings.HasPrefix(word, prefix+".")
}

func matchesSelector(labels, selector map[string]string) bool {
	for key, want := range selector {
		if labels[key] != want {
			return false
		}
	}
	return true
}

// collectTopology fetches services and resolves them against the pods already collected.
func (i *Inspector) collectTopology(ctx context.Context, rke2 *RKE2, fail func(string, error)) {
	data, err := i.kubectlGet(ctx, "services", "-A")
	if err != nil {
		fail("services", err)
		return
	}
	services, err := parseServices(data)
	if err != nil {
		fail("services", err)
		return
	}
	rke2.KubeServices = services
	linkTopology(rke2)
}

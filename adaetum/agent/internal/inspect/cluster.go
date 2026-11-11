package inspect

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// Event is a Kubernetes Warning event. Normal events are excluded: they are
// noise for a "what is wrong" view.
type Event struct {
	Namespace string `json:"namespace"`
	// Object is "Kind/name" of the object the event is about.
	Object  string `json:"object"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Count   int    `json:"count"`
	// LastSeen is an RFC 3339 UTC timestamp, so it sorts as a string.
	LastSeen string `json:"lastSeen"`
}

type Ingress struct {
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Class     string   `json:"class,omitempty"`
	Hosts     []string `json:"hosts"`
	TLS       bool     `json:"tls"`
	// Routes say which service each host and path is sent to.
	Routes []Route `json:"routes,omitempty"`
}

// Certificate is a cert-manager Certificate. NotAfter is RFC 3339, empty until
// the certificate has been issued.
type Certificate struct {
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Ready     bool     `json:"ready"`
	NotAfter  string   `json:"notAfter,omitempty"`
	DNSNames  []string `json:"dnsNames,omitempty"`
	Message   string   `json:"message,omitempty"`
}

const (
	maxEvents       = 50
	maxEventMessage = 300
	// kubectlConcurrency bounds simultaneous queries. Over SSH they share one
	// multiplexed connection; the cap protects the host and the API server.
	kubectlConcurrency = 4
)

// errorSink collects failures from concurrent queries. Order is made stable at
// the end so identical failures produce identical reports.
type errorSink struct {
	mutex  sync.Mutex
	errors []string
}

func (sink *errorSink) add(what string, err error) {
	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	sink.errors = append(sink.errors, what+": "+err.Error())
}

func (sink *errorSink) sorted() []string {
	sort.Strings(sink.errors)
	return sink.errors
}

// collectCluster runs every cluster query. Each writes a distinct field of
// rke2 and reports its own failure, so one bad query never hides the others.
func (i *Inspector) collectCluster(ctx context.Context, rke2 *RKE2, fail func(string, error)) {
	runConcurrently(kubectlConcurrency,
		query(i, ctx, fail, "nodes", &rke2.Nodes, parseNodes, "nodes"),
		query(i, ctx, fail, "workloads", &rke2.Workloads, parseWorkloads, "deployments,statefulsets,daemonsets", "-A"),
		query(i, ctx, fail, "pods", &rke2.Pods, parsePods, "pods", "-A"),
		query(i, ctx, fail, "argo cd applications", &rke2.ArgoApps, parseArgoApps, "applications.argoproj.io", "-A"),
		query(i, ctx, fail, "events", &rke2.Events, parseEvents, "events", "-A", "--field-selector", "type=Warning"),
		query(i, ctx, fail, "ingresses", &rke2.Ingresses, parseIngresses, "ingresses", "-A"),
		query(i, ctx, fail, "certificates", &rke2.Certificates, parseCertificates, "certificates.cert-manager.io", "-A"),
		query(i, ctx, fail, "autoscalers", &rke2.Autoscalers, parseAutoscalers, "horizontalpodautoscalers", "-A"),
		func() { rke2.Volumes = i.collectVolumes(ctx, fail) },
		func() { rke2.StorageCapacity = i.collectCapacity(ctx, fail) },
	)
	// Usage is merged into the pods, so it waits for the pod query to finish.
	i.collectUsage(ctx, rke2, fail)
	i.collectNodeUsage(ctx, rke2, fail)
	// Services are linked to pods, so they wait for the pod query too.
	i.collectTopology(ctx, rke2, fail)
}

// query returns a task that fetches one resource kind and stores the parsed
// result in dest. The generic helper keeps each query to a single line.
func query[T any](i *Inspector, ctx context.Context, fail func(string, error), label string,
	dest *[]T, parse func([]byte) ([]T, error), resources string, extra ...string) func() {
	return func() {
		data, err := i.kubectlGet(ctx, resources, extra...)
		if err != nil {
			fail(label, err)
			return
		}
		items, err := parse(data)
		if err != nil {
			fail(label, err)
			return
		}
		*dest = items
	}
}

func runConcurrently(limit int, tasks ...func()) {
	slots := make(chan struct{}, limit)
	var group sync.WaitGroup
	for _, task := range tasks {
		group.Add(1)
		slots <- struct{}{}
		go func(task func()) {
			defer group.Done()
			defer func() { <-slots }()
			task()
		}(task)
	}
	group.Wait()
}

// parseEvents reads Warning events, newest first, capped so a noisy cluster
// cannot bloat every report.
func parseEvents(data []byte) ([]Event, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace         string `json:"namespace"`
				CreationTimestamp string `json:"creationTimestamp"`
			} `json:"metadata"`
			InvolvedObject struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"involvedObject"`
			Reason        string `json:"reason"`
			Message       string `json:"message"`
			Count         int    `json:"count"`
			LastTimestamp string `json:"lastTimestamp"`
			EventTime     string `json:"eventTime"`
			Series        *struct {
				Count            int    `json:"count"`
				LastObservedTime string `json:"lastObservedTime"`
			} `json:"series"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	events := make([]Event, 0, len(list.Items))
	for _, item := range list.Items {
		// Events come in an old (lastTimestamp) and a newer (eventTime/series)
		// shape; take whichever is present.
		lastSeen := firstNonEmpty(item.LastTimestamp, item.EventTime, item.Metadata.CreationTimestamp)
		count := item.Count
		if item.Series != nil {
			lastSeen = firstNonEmpty(item.Series.LastObservedTime, lastSeen)
			count = item.Series.Count
		}
		message := item.Message
		if runes := []rune(message); len(runes) > maxEventMessage {
			message = string(runes[:maxEventMessage]) + "…"
		}
		events = append(events, Event{
			Namespace: item.Metadata.Namespace,
			Object:    item.InvolvedObject.Kind + "/" + item.InvolvedObject.Name,
			Reason:    item.Reason,
			Message:   message,
			Count:     max(count, 1),
			LastSeen:  lastSeen,
		})
	}

	sort.SliceStable(events, func(a, b int) bool { return events[a].LastSeen > events[b].LastSeen })
	if len(events) > maxEvents {
		events = events[:maxEvents]
	}
	return events, nil
}

func parseIngresses(data []byte) ([]Ingress, error) {
	var list struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				IngressClassName *string `json:"ingressClassName"`
				// Tailscale ingresses have no rules, only a default backend.
				DefaultBackend struct {
					Service struct {
						Name string `json:"name"`
					} `json:"service"`
				} `json:"defaultBackend"`
				Rules []struct {
					Host string `json:"host"`
					HTTP struct {
						Paths []struct {
							Path    string `json:"path"`
							Backend struct {
								Service struct {
									Name string `json:"name"`
								} `json:"service"`
							} `json:"backend"`
						} `json:"paths"`
					} `json:"http"`
				} `json:"rules"`
				TLS []json.RawMessage `json:"tls"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	ingresses := make([]Ingress, 0, len(list.Items))
	for _, item := range list.Items {
		ingress := Ingress{
			Namespace: item.Metadata.Namespace,
			Name:      item.Metadata.Name,
			Hosts:     []string{},
			TLS:       len(item.Spec.TLS) > 0,
		}
		if item.Spec.IngressClassName != nil {
			ingress.Class = *item.Spec.IngressClassName
		}
		if name := item.Spec.DefaultBackend.Service.Name; name != "" {
			ingress.Routes = append(ingress.Routes, Route{Service: name})
		}
		for _, rule := range item.Spec.Rules {
			if rule.Host != "" {
				ingress.Hosts = append(ingress.Hosts, rule.Host)
			}
			for _, path := range rule.HTTP.Paths {
				if name := path.Backend.Service.Name; name != "" {
					ingress.Routes = append(ingress.Routes, Route{Host: rule.Host, Path: path.Path, Service: name})
				}
			}
		}
		ingresses = append(ingresses, ingress)
	}
	return ingresses, nil
}

// parseCertificates reads cert-manager Certificates, soonest expiry first.
// Certificates that are not yet issued (no NotAfter) sort first because they
// are the ones needing attention.
func parseCertificates(data []byte) ([]Certificate, error) {
	var list struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				DNSNames []string `json:"dnsNames"`
			} `json:"spec"`
			Status struct {
				NotAfter   string `json:"notAfter"`
				Conditions []struct {
					Type    string `json:"type"`
					Status  string `json:"status"`
					Message string `json:"message"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	certificates := make([]Certificate, 0, len(list.Items))
	for _, item := range list.Items {
		certificate := Certificate{
			Namespace: item.Metadata.Namespace,
			Name:      item.Metadata.Name,
			NotAfter:  item.Status.NotAfter,
			DNSNames:  item.Spec.DNSNames,
		}
		for _, condition := range item.Status.Conditions {
			if condition.Type == "Ready" {
				certificate.Ready = condition.Status == "True"
				if !certificate.Ready {
					certificate.Message = condition.Message
				}
			}
		}
		certificates = append(certificates, certificate)
	}
	sort.SliceStable(certificates, func(a, b int) bool { return certificates[a].NotAfter < certificates[b].NotAfter })
	return certificates, nil
}

// parseFailedUnits reads `systemctl --failed --no-legend --plain`, one unit per
// line. A leading status bullet is stripped when systemd prints one.
func parseFailedUnits(output string) []string {
	units := []string{}
	for _, line := range strings.Split(output, "\n") {
		if fields := strings.Fields(strings.TrimLeft(line, "●* ")); len(fields) > 0 {
			units = append(units, fields[0])
		}
	}
	return units
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

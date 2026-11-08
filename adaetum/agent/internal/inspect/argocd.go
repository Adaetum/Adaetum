package inspect

import "encoding/json"

// ArgoApp is the state of one Argo CD Application. Argo CD reconciles the
// product stack from `pods/`, so this is the "is the cluster converged" view.
type ArgoApp struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Project   string `json:"project"`
	// Sync is Synced, OutOfSync, or Unknown. Health is Healthy, Progressing,
	// Degraded, Suspended, Missing, or Unknown.
	Sync   string `json:"sync"`
	Health string `json:"health"`
	// Revision is the synced revision. A git commit SHA is shortened to seven
	// characters; other revisions (such as a Helm chart version) are kept whole.
	Revision string `json:"revision,omitempty"`
	// Operation is the phase of the last sync operation (Succeeded, Failed,
	// Running, ...). Message explains a failed operation or a condition such as
	// a comparison error; it is empty for healthy applications.
	Operation string `json:"operation,omitempty"`
	Message   string `json:"message,omitempty"`
	// DestNamespace is where the app deploys; Chart and ChartRepo name the Helm chart
	// when it is one (empty for git apps). They let the page find an app's own logo.
	DestNamespace string `json:"destNamespace,omitempty"`
	Chart         string `json:"chart,omitempty"`
	ChartRepo     string `json:"chartRepo,omitempty"`
}

// argoSource is the part of an Argo CD source the report uses.
type argoSource struct {
	Chart   string `json:"chart"`
	RepoURL string `json:"repoURL"`
}

// parseArgoApps reads `kubectl get applications.argoproj.io -A -o json`.
func parseArgoApps(data []byte) ([]ArgoApp, error) {
	var list struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				Project     string `json:"project"`
				Destination struct {
					Namespace string `json:"namespace"`
				} `json:"destination"`
				Source  argoSource   `json:"source"`
				Sources []argoSource `json:"sources"`
			} `json:"spec"`
			Status struct {
				Sync struct {
					Status   string `json:"status"`
					Revision string `json:"revision"`
				} `json:"sync"`
				Health struct {
					Status string `json:"status"`
				} `json:"health"`
				OperationState struct {
					Phase   string `json:"phase"`
					Message string `json:"message"`
				} `json:"operationState"`
				Conditions []struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	apps := make([]ArgoApp, 0, len(list.Items))
	for _, item := range list.Items {
		app := ArgoApp{
			Namespace: item.Metadata.Namespace,
			Name:      item.Metadata.Name,
			Project:   item.Spec.Project,
			Sync:      orUnknown(item.Status.Sync.Status),
			Health:    orUnknown(item.Status.Health.Status),
			Operation: item.Status.OperationState.Phase,
		}
		app.Revision = shortRevision(item.Status.Sync.Revision)
		app.DestNamespace = item.Spec.Destination.Namespace
		for _, source := range append([]argoSource{item.Spec.Source}, item.Spec.Sources...) {
			if source.Chart != "" {
				app.Chart, app.ChartRepo = source.Chart, source.RepoURL
				break
			}
		}

		// Prefer a condition (for example ComparisonError) because it names the
		// current problem; fall back to the last operation's message only when
		// that operation did not succeed.
		switch {
		case len(item.Status.Conditions) > 0:
			app.Message = item.Status.Conditions[0].Message
		case item.Status.OperationState.Phase != "" && item.Status.OperationState.Phase != "Succeeded":
			app.Message = item.Status.OperationState.Message
		}
		apps = append(apps, app)
	}
	return apps, nil
}

// shortRevision shortens full git SHAs and leaves every other revision alone,
// because truncating a chart version like "2026.5.1" would make it wrong.
func shortRevision(revision string) string {
	if (len(revision) == 40 || len(revision) == 64) && isHex(revision) {
		return revision[:7]
	}
	return revision
}

func isHex(value string) bool {
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func orUnknown(value string) string {
	if value == "" {
		return "Unknown"
	}
	return value
}

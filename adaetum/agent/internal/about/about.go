// Package about serves the "About this app" card on an app's page.
//
// SOURCE OF TRUTH. The text lives in the cluster repository, next to the app's Argo CD
// definition: pods/<namespace>/<app>.about.yaml. A person or an AI assistant (the
// describe-pod-app skill) writes it, and it is reviewed and merged like any other change,
// so nothing here is ever generated at run time and the agent never writes it. The agent
// only reads it back from Gitea, with the same read credential it already holds.
//
// WHY A SMALL FIXED SHAPE. The card is plain text that a model drafted from an app's
// manifests and chart, so Parse rejects anything outside the shape (unknown fields, too
// much text) instead of trying to render it. The web page shows every field as text, never
// as markup.
package about

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// FileSuffix ends the name of an about file: <app>.about.yaml, beside <app>.app.yaml.
const FileSuffix = ".about.yaml"

// Limits keep the card short enough to read and bound what a draft can put on the page.
const (
	maxSummary   = 400
	maxFacts     = 8
	maxLabel     = 40
	maxValue     = 300
	maxWatch     = 5
	maxWatchText = 200
	maxFileBytes = 16 << 10
)

// Fact is one labelled line, such as "Where its data lives".
type Fact struct {
	Label string `yaml:"label" json:"label"`
	Value string `yaml:"value" json:"value"`
}

// Document is one app's description.
type Document struct {
	// Summary says what the app is for, in a sentence or two.
	Summary string `yaml:"summary" json:"summary"`
	// Facts are the other things worth knowing: how it is reached, where its data lives,
	// what to back up.
	Facts []Fact `yaml:"facts" json:"facts,omitempty"`
	// Watch lists things that tell an operator the app is unhealthy.
	Watch []string `yaml:"watch" json:"watch,omitempty"`
	// Written says who drafted it and when, so a reader can judge how far to trust it.
	Written Written `yaml:"written" json:"written"`
}

// Written is where the text came from.
type Written struct {
	// By is "person" or "assistant".
	By string `yaml:"by" json:"by"`
	// Date is when it was written, YYYY-MM-DD. (Not called "on": YAML 1.1 readers turn
	// an unquoted on key into true.)
	Date string `yaml:"date" json:"date"`
	// Version is the chart or image version the text was written against, so a later
	// upgrade is a visible reason to check it again.
	Version string `yaml:"version" json:"version,omitempty"`
}

// Parse reads and checks one about file.
func Parse(data []byte) (Document, error) {
	if len(data) > maxFileBytes {
		return Document{}, errors.New("the about file is too large")
	}
	var doc Document
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("the about file is not valid: %w", err)
	}
	if err := doc.check(); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func (d Document) check() error {
	if err := text("summary", d.Summary, maxSummary, true); err != nil {
		return err
	}
	if len(d.Facts) > maxFacts {
		return fmt.Errorf("facts: at most %d", maxFacts)
	}
	for _, fact := range d.Facts {
		if err := text("a fact's label", fact.Label, maxLabel, true); err != nil {
			return err
		}
		if err := text("a fact's value", fact.Value, maxValue, true); err != nil {
			return err
		}
	}
	if len(d.Watch) > maxWatch {
		return fmt.Errorf("watch: at most %d", maxWatch)
	}
	for _, item := range d.Watch {
		if err := text("a watch item", item, maxWatchText, true); err != nil {
			return err
		}
	}
	if d.Written.By != "person" && d.Written.By != "assistant" {
		return errors.New(`written.by must be "person" or "assistant"`)
	}
	if len(d.Written.Date) != len("2006-01-02") || d.Written.Date[4] != '-' || d.Written.Date[7] != '-' {
		return errors.New("written.date must be a date, YYYY-MM-DD")
	}
	return text("written.version", d.Written.Version, 60, false)
}

func text(name, value string, limit int, required bool) error {
	value = strings.TrimSpace(value)
	switch {
	case value == "" && required:
		return fmt.Errorf("%s is empty", name)
	case utf8.RuneCountInString(value) > limit:
		return fmt.Errorf("%s is longer than %d characters", name, limit)
	case strings.ContainsAny(value, "<>"):
		// The page renders text only, but a draft with markup in it is a mistake worth catching.
		return fmt.Errorf("%s contains markup", name)
	}
	return nil
}

// Name is the app name an about file path is for: "pods/media/plex.about.yaml" is "plex".
// It is false for any other path.
func Name(path string) (string, bool) {
	if !strings.HasPrefix(path, "pods/") || !strings.HasSuffix(path, FileSuffix) {
		return "", false
	}
	base := path[strings.LastIndex(path, "/")+1:]
	name := strings.TrimSuffix(base, FileSuffix)
	return name, name != ""
}

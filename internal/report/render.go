package report

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// WriteYAML writes the document as YAML.
func WriteYAML(w io.Writer, doc *Document) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return enc.Close()
}

// MarshalYAML writes a step as `<keyword>: <text>` followed by its fields, and
// nested Thens as `then: [{text: ...}]`.
func (s *Step) MarshalYAML() (any, error) {
	return s.node(s.Keyword), nil
}

func (s *Step) node(key string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	add := func(k, v string) {
		if v == "" {
			return
		}
		n.Content = append(n.Content, scalar(k), scalar(v))
	}
	add(key, s.Text)
	add("status", s.Status)
	add("duration", s.Duration)
	add("error", s.Error)
	add("log", s.Log)
	if len(s.Then) > 0 {
		list := &yaml.Node{Kind: yaml.SequenceNode}
		for _, th := range s.Then {
			list.Content = append(list.Content, th.node("text"))
		}
		n.Content = append(n.Content, scalar("then"), list)
	}
	return n
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// WriteCases writes the case inventory in the row format of `signet cases`:
// one GROUP row per feature, one CASE row per scenario, and a total.
func WriteCases(w io.Writer, doc *Document) {
	cases, steps := 0, 0
	for _, f := range doc.Features {
		fmt.Fprintf(w, "GROUP %s %s %s\n", f.Package, f.Test, f.Feature)
		for _, s := range f.Scenarios {
			n := s.StepCount()
			fmt.Fprintf(w, "CASE id=%s name=%q (%s)\n", s.ID, s.Name, plural(n, "step"))
			cases++
			steps += n
		}
	}
	fmt.Fprintf(w, "%s, %s, %s\n", plural(len(doc.Features), "group"), plural(cases, "case"), plural(steps, "step"))
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

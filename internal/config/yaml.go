package config

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

const configHeaderComment = `Secret Protector configuration.
This file contains secrets; keep file mode 0600.
Edit this YAML directly or use the management commands. Existing comments are preserved on updates.`

const routesComment = `Proxy routes. An empty list keeps the proxy unready.
Add routes with "secret-protector route add", or replace [] manually using this shape:
  - name: example
    upstream:
      url: https://api.example.com/v1
      auth:
        mode: bearer
        token: REPLACE_ME_UPSTREAM_SECRET
    downstream:
      query_params: [token]
      tokens:
        - name: local-client
          value: REPLACE_ME_DOWNSTREAM_TOKEN
auth.mode may be auto, bearer, query, or basic.
auth.query_param is used by query/auto; username and password are used by basic/auto.`

func marshalConfig(cfg *Config) ([]byte, error) {
	desired, err := configNode(cfg)
	if err != nil {
		return nil, err
	}

	document := desired
	if len(cfg.source) == 0 {
		addGeneratedComments(document)
	} else {
		source, parseErr := parseYAMLNode(cfg.source)
		if parseErr != nil {
			return nil, fmt.Errorf("parse source config: %w", parseErr)
		}
		document = mergeYAMLNode(desired, source)
	}

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("close config encoder: %w", err)
	}

	return output.Bytes(), nil
}

func configNode(cfg *Config) (*yaml.Node, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	document, err := parseYAMLNode(data)
	if err != nil {
		return nil, fmt.Errorf("parse generated config: %w", err)
	}

	return document, nil
}

func parseYAMLNode(data []byte) (*yaml.Node, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}

	return &document, nil
}

func addGeneratedComments(document *yaml.Node) {
	root := yamlRoot(document)
	if root == nil {
		return
	}

	versionKey, version := mappingEntry(root, "version")
	if versionKey != nil {
		versionKey.HeadComment = configHeaderComment
	}
	if version != nil {
		version.LineComment = "schema version; currently only 1 is supported"
	}

	serverKey, server := mappingEntry(root, "server")
	if serverKey != nil {
		serverKey.HeadComment = "HTTP server settings. Restart the process after changing any value below."
	}
	addFieldComment(server, "listen", "TCP address for downstream clients.")
	addFieldComment(server, "reload_interval", "How often the configuration file is checked for route changes.")
	addFieldComment(server, "read_header_timeout", "Maximum time allowed for reading request headers.")
	addFieldComment(server, "idle_timeout", "Maximum keep-alive idle time.")
	addFieldComment(server, "shutdown_timeout", "Maximum graceful shutdown time.")

	routesKey, _ := mappingEntry(root, "routes")
	if routesKey != nil {
		routesKey.HeadComment = routesComment
	}
}

func addFieldComment(mapping *yaml.Node, field string, comment string) {
	key, _ := mappingEntry(mapping, field)
	if key == nil {
		return
	}
	key.HeadComment = comment
}

func yamlRoot(document *yaml.Node) *yaml.Node {
	if document == nil {
		return nil
	}
	if document.Kind != yaml.DocumentNode {
		return document
	}
	if len(document.Content) == 0 {
		return nil
	}

	return document.Content[0]
}

func mappingEntry(mapping *yaml.Node, field string) (*yaml.Node, *yaml.Node) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil
	}

	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		if key.Value == field {
			return key, mapping.Content[index+1]
		}
	}

	return nil, nil
}

func mergeYAMLNode(desired *yaml.Node, source *yaml.Node) *yaml.Node {
	if desired == nil {
		return nil
	}
	if source == nil || desired.Kind != source.Kind {
		return cloneYAMLNode(desired)
	}

	merged := cloneYAMLNode(desired)
	copyYAMLPresentation(merged, source)

	switch desired.Kind {
	case yaml.DocumentNode:
		merged.Content = mergeDocumentContent(desired, source)
	case yaml.MappingNode:
		merged.Content = mergeMappingContent(desired, source)
	case yaml.SequenceNode:
		merged.Content = mergeSequenceContent(desired, source)
	}

	return merged
}

func mergeDocumentContent(desired *yaml.Node, source *yaml.Node) []*yaml.Node {
	if len(desired.Content) == 0 {
		return nil
	}
	if len(source.Content) == 0 {
		return []*yaml.Node{cloneYAMLNode(desired.Content[0])}
	}

	return []*yaml.Node{mergeYAMLNode(desired.Content[0], source.Content[0])}
}

func mergeMappingContent(desired *yaml.Node, source *yaml.Node) []*yaml.Node {
	content := make([]*yaml.Node, 0, len(desired.Content))
	used := make(map[int]struct{}, len(desired.Content)/2)

	for sourceIndex := 0; sourceIndex+1 < len(source.Content); sourceIndex += 2 {
		desiredIndex := mappingIndex(desired, source.Content[sourceIndex])
		if desiredIndex < 0 {
			continue
		}
		used[desiredIndex] = struct{}{}
		content = append(content,
			mergeYAMLNode(desired.Content[desiredIndex], source.Content[sourceIndex]),
			mergeYAMLNode(desired.Content[desiredIndex+1], source.Content[sourceIndex+1]),
		)
	}

	for desiredIndex := 0; desiredIndex+1 < len(desired.Content); desiredIndex += 2 {
		if _, ok := used[desiredIndex]; ok {
			continue
		}
		content = append(content,
			cloneYAMLNode(desired.Content[desiredIndex]),
			cloneYAMLNode(desired.Content[desiredIndex+1]),
		)
	}

	return content
}

func mappingIndex(mapping *yaml.Node, key *yaml.Node) int {
	if mapping == nil || mapping.Kind != yaml.MappingNode || key == nil {
		return -1
	}

	for index := 0; index+1 < len(mapping.Content); index += 2 {
		candidate := mapping.Content[index]
		if candidate.Kind == key.Kind && candidate.Tag == key.Tag && candidate.Value == key.Value {
			return index
		}
	}

	return -1
}

func mergeSequenceContent(desired *yaml.Node, source *yaml.Node) []*yaml.Node {
	content := make([]*yaml.Node, 0, len(desired.Content))
	used := make(map[int]struct{}, len(source.Content))

	for desiredIndex, desiredItem := range desired.Content {
		sourceIndex := sequenceIndex(source, desiredItem, desiredIndex, used)
		if sourceIndex < 0 {
			content = append(content, cloneYAMLNode(desiredItem))
			continue
		}
		used[sourceIndex] = struct{}{}
		content = append(content, mergeYAMLNode(desiredItem, source.Content[sourceIndex]))
	}

	return content
}

func sequenceIndex(sequence *yaml.Node, desired *yaml.Node, preferred int, used map[int]struct{}) int {
	identity := yamlNodeIdentity(desired)
	if identity != "" {
		for index, candidate := range sequence.Content {
			if _, ok := used[index]; ok {
				continue
			}
			if yamlNodeIdentity(candidate) == identity {
				return index
			}
		}
		return -1
	}

	if preferred >= len(sequence.Content) {
		return -1
	}
	if _, ok := used[preferred]; ok {
		return -1
	}
	if sequence.Content[preferred].Kind != desired.Kind {
		return -1
	}

	return preferred
}

func yamlNodeIdentity(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	if node.Kind == yaml.ScalarNode {
		return node.Tag + "\x00" + node.Value
	}
	if node.Kind != yaml.MappingNode {
		return ""
	}

	_, name := mappingEntry(node, "name")
	if name == nil || name.Kind != yaml.ScalarNode {
		return ""
	}

	return "name\x00" + name.Value
}

func copyYAMLPresentation(target *yaml.Node, source *yaml.Node) {
	if source.HeadComment != "" {
		target.HeadComment = source.HeadComment
	}
	if source.LineComment != "" {
		target.LineComment = source.LineComment
	}
	if source.FootComment != "" {
		target.FootComment = source.FootComment
	}
	if len(source.Content) == 0 && len(target.Content) > 0 {
		return
	}
	if target.Kind != yaml.ScalarNode || target.Tag == source.Tag {
		target.Style = source.Style
	}
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}

	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		clone.Content[index] = cloneYAMLNode(child)
	}

	return &clone
}

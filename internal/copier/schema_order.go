package copier

import (
	"fmt"
	"sort"
	"strings"
)

type schemaObjectRef struct {
	kind      string
	index     int
	key       string
	name      string
	dependsOn []string
}

func (c *copier) schemaCreationOrder() ([]schemaObjectRef, error) {
	nodes := make([]schemaObjectRef, 0, len(c.tables)+len(c.views)+len(c.functions)+len(c.synonyms)+len(c.procedures))
	add := func(kind string, index int, schema string, name string, dependencies []string) {
		nodes = append(nodes, schemaObjectRef{
			kind:      kind,
			index:     index,
			key:       objectKey(schema, name),
			name:      quoteIdent(schema) + "." + quoteIdent(name),
			dependsOn: dependencies,
		})
	}
	for i, table := range c.tables {
		add("table", i, table.Schema, table.Name, table.DependsOn)
	}
	for i, view := range c.views {
		add("view", i, view.Schema, view.Name, view.DependsOn)
	}
	for i, function := range c.functions {
		add("function", i, function.Schema, function.Name, function.DependsOn)
	}
	for i, synonym := range c.synonyms {
		add("synonym", i, synonym.Schema, synonym.Name, nil)
	}
	for i, procedure := range c.procedures {
		add("procedure", i, procedure.Schema, procedure.Name, procedure.DependsOn)
	}

	indexByKey := make(map[string]int, len(nodes))
	for i := range nodes {
		indexByKey[nodes[i].key] = i
	}
	inDegree := make([]int, len(nodes))
	adjacent := make([][]int, len(nodes))
	for i, node := range nodes {
		seen := make(map[int]struct{})
		for _, dependency := range node.dependsOn {
			dependencyIndex, ok := indexByKey[strings.ToLower(dependency)]
			if !ok || dependencyIndex == i {
				continue
			}
			if _, duplicate := seen[dependencyIndex]; duplicate {
				continue
			}
			seen[dependencyIndex] = struct{}{}
			adjacent[dependencyIndex] = append(adjacent[dependencyIndex], i)
			inDegree[i]++
		}
	}

	queue := make([]int, 0, len(nodes))
	for i, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, i)
		}
	}
	sortSchemaQueue(queue, nodes)

	ordered := make([]schemaObjectRef, 0, len(nodes))
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		ordered = append(ordered, nodes[current])
		for _, next := range adjacent[current] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
				sortSchemaQueue(queue, nodes)
			}
		}
	}
	if len(ordered) != len(nodes) {
		var cycle []string
		for i, degree := range inDegree {
			if degree > 0 {
				cycle = append(cycle, nodes[i].kind+" "+nodes[i].name)
			}
		}
		sort.Strings(cycle)
		return nil, fmt.Errorf("circular dependency detected among schema objects: %s", strings.Join(cycle, ", "))
	}
	return ordered, nil
}

func sortSchemaQueue(queue []int, nodes []schemaObjectRef) {
	sort.Slice(queue, func(i, j int) bool {
		left := nodes[queue[i]]
		right := nodes[queue[j]]
		leftRank := schemaObjectKindRank(left.kind)
		rightRank := schemaObjectKindRank(right.kind)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return strings.ToLower(left.name) < strings.ToLower(right.name)
	})
}

func schemaObjectKindRank(kind string) int {
	switch kind {
	case "table":
		return 0
	case "view":
		return 1
	case "function":
		return 2
	case "synonym":
		return 3
	case "procedure":
		return 4
	default:
		return 5
	}
}

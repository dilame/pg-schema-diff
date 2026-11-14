package graph

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"
)

type Vertex interface {
	GetId() string
}

type AdjacencyMatrix map[string]map[string]bool

// CycleError is returned by a topological sort of a graph that has a cycle.
type CycleError struct {
	// OnCycle holds the ids of the vertices that lie on a cycle, sorted.
	OnCycle []string
	message string
}

func (e *CycleError) Error() string {
	return e.message
}

// verticesOnCycles returns the ids of the vertices that lie on a cycle: the members of every
// strongly connected component with more than one vertex, and every vertex with an edge to itself.
func (g *Graph[V]) verticesOnCycles() []string {
	ids := make([]string, 0, len(g.verticesById))
	for id := range g.verticesById {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// Tarjan's algorithm.
	index := 0
	indexById := make(map[string]int)
	lowLinkById := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var onCycles []string

	var connect func(id string)
	connect = func(id string) {
		indexById[id] = index
		lowLinkById[id] = index
		index++
		stack = append(stack, id)
		onStack[id] = true

		var targets []string
		for target, hasEdge := range g.edges[id] {
			if hasEdge {
				if _, ok := g.verticesById[target]; ok {
					targets = append(targets, target)
				}
			}
		}
		sort.Strings(targets)
		for _, target := range targets {
			if _, visited := indexById[target]; !visited {
				connect(target)
				lowLinkById[id] = min(lowLinkById[id], lowLinkById[target])
			} else if onStack[target] {
				lowLinkById[id] = min(lowLinkById[id], indexById[target])
			}
		}

		if lowLinkById[id] == indexById[id] {
			var component []string
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[top] = false
				component = append(component, top)
				if top == id {
					break
				}
			}
			if len(component) > 1 || g.edges[id][id] {
				onCycles = append(onCycles, component...)
			}
		}
	}
	for _, id := range ids {
		if _, visited := indexById[id]; !visited {
			connect(id)
		}
	}
	sort.Strings(onCycles)
	return onCycles
}

// Graph is a directed graph
type Graph[V Vertex] struct {
	verticesById map[string]V
	edges        AdjacencyMatrix
}

func NewGraph[V Vertex]() *Graph[V] {
	return &Graph[V]{
		verticesById: make(map[string]V),
		edges:        make(AdjacencyMatrix),
	}
}

// AddVertex adds a vertex to the graph.
// If the vertex already exists, it will override it and keep the edges
func (g *Graph[V]) AddVertex(v V) {
	g.verticesById[v.GetId()] = v
	if g.edges[v.GetId()] == nil {
		g.edges[v.GetId()] = make(map[string]bool)
	}
}

// AddEdge adds an edge to the graph. If the vertex doesn't exist, it will error
func (g *Graph[V]) AddEdge(sourceId, targetId string) error {
	if !g.HasVertexWithId(sourceId) {
		return fmt.Errorf("source %s does not exist", sourceId)
	}
	if !g.HasVertexWithId(targetId) {
		return fmt.Errorf("target %s does not exist", targetId)
	}
	g.edges[sourceId][targetId] = true

	return nil
}

// Union unions the graph with a new graph. If a vertex exists in both graphs,
// it uses the merge function to determine what the new vertex is
func (g *Graph[V]) Union(new *Graph[V], merge func(old, new V) V) error {
	for _, newV := range new.verticesById {
		if g.HasVertexWithId(newV.GetId()) {
			id := newV.GetId()
			// merge the vertices using the procedure defined by the user
			newV = merge(g.GetVertex(newV.GetId()), newV)
			if newV.GetId() != id {
				return fmt.Errorf("the merge function must return a vertex with the same id: "+
					"expected %s but found %s", id, newV.GetId())
			}
		}
		g.AddVertex(newV)
	}

	for source, adjacentEdgesMap := range new.edges {
		for target, isAdjacent := range adjacentEdgesMap {
			if isAdjacent {
				if err := g.AddEdge(source, target); err != nil {
					return fmt.Errorf("adding an edge from the new graph: %w", err)
				}
			}
		}
	}

	return nil
}

func (g *Graph[V]) GetVertex(id string) V {
	return g.verticesById[id]
}

func (g *Graph[V]) HasVertexWithId(id string) bool {
	_, hasVertex := g.verticesById[id]
	return hasVertex
}

// Reverse reverses the edges of the map. The sources become the sinks and vice versa.
func (g *Graph[V]) Reverse() {
	reversedEdges := make(AdjacencyMatrix)
	for vertexId := range g.verticesById {
		reversedEdges[vertexId] = make(map[string]bool)
	}
	for source, adjacentEdgesMap := range g.edges {
		for target, isAdjacent := range adjacentEdgesMap {
			if isAdjacent {
				reversedEdges[target][source] = true
			}
		}
	}
	g.edges = reversedEdges
}

func (g *Graph[V]) Copy() *Graph[V] {
	verticesById := make(map[string]V)
	for id, v := range g.verticesById {
		verticesById[id] = v
	}

	edges := make(AdjacencyMatrix)
	for source, adjacentEdgesMap := range g.edges {
		edges[source] = make(map[string]bool)
		for target, isAdjacent := range adjacentEdgesMap {
			edges[source][target] = isAdjacent
		}
	}

	return &Graph[V]{
		verticesById: verticesById,
		edges:        edges,
	}
}

func (g *Graph[V]) TopologicallySort() ([]V, error) {
	return g.TopologicallySortWithPriority(func(_, _ V) bool {
		return false
	})
}

type Ordered interface {
	~int | ~string
}

func IsLowerPriorityFromGetPriority[V Vertex, P Ordered](getPriority func(V) P) func(V, V) bool {
	return func(v1 V, v2 V) bool {
		return getPriority(v1) < getPriority(v2)
	}
}

// sourceQueue is the set of vertices a topological sort can emit next, ordered so that heap.Pop
// returns the one the greedy rule chooses: the vertex that is not lower priority than any other
// available one, and the smaller id when neither is lower than the other. The id kept beside the
// vertex is the graph's own key, so the comparison never has to format the vertex again.
type sourceQueue[V Vertex] struct {
	entries         []sourceEntry[V]
	isLowerPriority func(V, V) bool
}

type sourceEntry[V Vertex] struct {
	id string
	v  V
}

func (q *sourceQueue[V]) Len() int {
	return len(q.entries)
}

func (q *sourceQueue[V]) Less(i, j int) bool {
	left, right := q.entries[i], q.entries[j]
	if q.isLowerPriority(left.v, right.v) {
		return false
	}
	if q.isLowerPriority(right.v, left.v) {
		return true
	}
	return left.id < right.id
}

func (q *sourceQueue[V]) Swap(i, j int) {
	q.entries[i], q.entries[j] = q.entries[j], q.entries[i]
}

func (q *sourceQueue[V]) Push(x any) {
	q.entries = append(q.entries, x.(sourceEntry[V]))
}

func (q *sourceQueue[V]) Pop() any {
	last := len(q.entries) - 1
	entry := q.entries[last]
	q.entries = q.entries[:last]
	return entry
}

// TopologicallySortWithPriority returns a topological sort of the graph taking a greedy approach to put
// high priority sources first: of the vertices no remaining edge points at, it emits the one no other is
// lower priority than, the smaller id breaking a tie.
//
// isLowerPriority must be a strict weak ordering — each vertex is not lower priority than itself, no two
// vertices are each lower than the other, and lower-than and its absence are both transitive. Under one,
// the emitted order is deterministic. With a comparator that is not, the order depends on the map
// iteration order the queue is seeded in, because the queue compares only the vertices it holds.
func (g *Graph[V]) TopologicallySortWithPriority(isLowerPriority func(V, V) bool) ([]V, error) {
	// This uses mutation. Copy the graph
	graph := g.Copy()

	// The strategy of this algorithm:
	// 1. Count the number of incoming edges to each vertex
	// 2. Emit the highest-priority source and add it to the output
	// 3. Decrement the number of incoming edges to each vertex adjacent to the emitted one
	// 4. Repeat 2-3 until the graph is empty
	incomingEdgeCountByVertex := make(map[string]int, len(graph.verticesById))
	for _, adjacentEdges := range graph.edges {
		for target, isAdjacent := range adjacentEdges {
			if isAdjacent {
				incomingEdgeCountByVertex[target]++
			}
		}
	}

	// A heap holds the sources, so a step costs a logarithmic number of comparisons instead of
	// a re-sort of every available source. The graph carries a vertex for every schema object,
	// no-op vertices included, so re-sorting the available ones at every step is quadratic in
	// the size of the schema.
	sources := &sourceQueue[V]{isLowerPriority: isLowerPriority}
	for id := range graph.verticesById {
		if incomingEdgeCountByVertex[id] == 0 {
			sources.entries = append(sources.entries, sourceEntry[V]{id: id, v: graph.GetVertex(id)})
		}
	}
	heap.Init(sources)

	var output []V
	for sources.Len() > 0 {
		source := heap.Pop(sources).(sourceEntry[V])
		output = append(output, source.v)

		// Emitting the source may free the vertices it pointed at.
		for target, hasEdge := range graph.edges[source.id] {
			if !hasEdge {
				continue
			}
			incomingEdgeCountByVertex[target]--
			if incomingEdgeCountByVertex[target] == 0 {
				heap.Push(sources, sourceEntry[V]{id: target, v: graph.GetVertex(target)})
			}
		}

		// Delete the emitted vertex from graph
		delete(graph.verticesById, source.id)
		// We don't need to worry about any incoming edges referencing this vertex, since it was a source
		delete(graph.edges, source.id)
		// The cycle diagnostic prints this map, so it must describe what remains, as the graph does.
		delete(incomingEdgeCountByVertex, source.id)
	}

	// Every vertex emitted means the graph had no cycle; what is left lies on one.
	if len(graph.verticesById) > 0 {
		dotSB := strings.Builder{}
		if err := EncodeDOT(g, &dotSB, true); err != nil {
			dotSB.Reset()
			dotSB.WriteString(fmt.Sprintf("failed to encode graph to DOT: %v", err))
		}
		return nil, &CycleError{
			OnCycle: graph.verticesOnCycles(),
			message: fmt.Sprintf("cycle detected: %+v, %+v\n%s", graph, incomingEdgeCountByVertex, dotSB.String()),
		}
	}

	return output, nil
}

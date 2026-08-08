// Package graph models Kubernetes resources and the logical relationships
// between them. It is the substrate that diagnostic rules and the causal
// correlation engine operate on.
package graph

import (
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilversion "k8s.io/apimachinery/pkg/util/version"
)

// ResourceRef uniquely identifies a Kubernetes resource inside the graph.
type ResourceRef struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

func (r ResourceRef) String() string {
	if r.Namespace == "" {
		return fmt.Sprintf("%s/%s", r.Kind, r.Name)
	}
	return fmt.Sprintf("%s/%s/%s", r.Kind, r.Namespace, r.Name)
}

// EdgeType describes the logical relationship an edge represents.
type EdgeType string

const (
	EdgeOwns        EdgeType = "owns"         // Deployment owns ReplicaSet, ReplicaSet owns Pod
	EdgeSelects     EdgeType = "selects"      // Service selects Pod
	EdgeRoutesTo    EdgeType = "routes-to"    // Service routes to EndpointSlice
	EdgeReferences  EdgeType = "references"   // Pod references ConfigMap/Secret
	EdgeScheduledOn EdgeType = "scheduled-on" // Pod scheduled on Node
)

// Node is a Kubernetes resource inside the graph. Object holds the typed
// API object (e.g. *corev1.Pod). Object is nil when the resource is
// referenced by another resource but does not exist in the cluster; rules
// use this to detect dangling references.
type Node struct {
	Ref     ResourceRef
	Object  any
	Unknown bool
}

// Missing reports whether the resource was referenced but not found.
func (n *Node) Missing() bool { return n.Object == nil && !n.Unknown }

// ExistenceUnknown reports that the collector deliberately did not or could
// not establish whether the referenced object exists. Unknown is distinct
// from missing so rules never turn an RBAC or collection gap into a finding.
func (n *Node) ExistenceUnknown() bool { return n.Object == nil && n.Unknown }

// Edge is a directed, typed relationship between two nodes.
type Edge struct {
	From ResourceRef
	To   ResourceRef
	Type EdgeType
}

// CollectionIssue records evidence the collector could not obtain. Issues are
// part of the diagnostic result: a failed API call must not be interpreted as
// proof that an object or endpoint is absent.
type CollectionIssue struct {
	Source        string      `json:"source"`
	Resource      ResourceRef `json:"resource"`
	Message       string      `json:"message"`
	AffectsHealth bool        `json:"affectsHealth"`
}

const (
	SourceEndpointSlices    = "endpointslices"
	SourceEvents            = "events"
	SourcePod               = "pod"
	SourceService           = "service"
	SourceReplicaSet        = "replicaset"
	SourceDeployment        = "deployment"
	SourceNode              = "node"
	SourceConfigMap         = "configmap"
	SourceSecret            = "secret"
	SourceServerVersion     = "kubernetes-version"
	SourceTemporalIntegrity = "temporal-integrity"
	SourceRuleCompatibility = "rule-compatibility"
	SourceRuleExecution     = "rule-execution"
)

// ClusterInfo records the Kubernetes API server version that supplied the
// graph. Major and Minor are kept as numbers so rule compatibility checks do
// not need to interpret vendor suffixes such as +k3s1 repeatedly.
type ClusterInfo struct {
	KubernetesVersion string `json:"kubernetesVersion,omitempty"`
	Major             uint   `json:"major,omitempty"`
	Minor             uint   `json:"minor,omitempty"`
}

// FocusRevision records the Kubernetes concurrency fields observed for the
// focus resource. ResourceVersion changes on any persisted object update;
// Generation changes when the desired specification changes.
type FocusRevision struct {
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Generation      int64  `json:"generation,omitempty"`
}

// FocusStability describes whether the focus resource stayed unchanged while
// related evidence was collected. It deliberately does not claim that all
// Kubernetes resource kinds form an atomic cluster-wide snapshot.
type FocusStability string

const (
	FocusStabilityUnknown FocusStability = "unknown"
	FocusStabilityStable  FocusStability = "stable"
	FocusStabilityChanged FocusStability = "changed"
)

// CollectionInfo bounds a live inspection in time and records the focus
// revision at both ends. Synthetic graphs leave this nil.
type CollectionInfo struct {
	StartedAt      time.Time      `json:"startedAt"`
	CompletedAt    time.Time      `json:"completedAt"`
	FocusStart     FocusRevision  `json:"focusStart"`
	FocusEnd       *FocusRevision `json:"focusEnd,omitempty"`
	FocusStability FocusStability `json:"focusStability"`
}

// Graph is a small in-memory resource relationship graph built around a
// single focus resource (the resource the user asked Tuna to inspect).
type Graph struct {
	Focus ResourceRef
	// Cluster is empty for synthetic graphs built directly by unit tests. Live
	// collectors always attempt version discovery and record a collection issue
	// when it fails.
	Cluster ClusterInfo
	// Collection is present for live collectors and records bounded temporal
	// evidence about the focus resource. It is not an atomic cluster snapshot.
	Collection *CollectionInfo

	nodes           map[string]*Node
	edges           []Edge
	events          []corev1.Event
	issues          []CollectionIssue
	collectionUID   types.UID
	collectionStart FocusRevision
}

// SetKubernetesVersion parses and records the API server's reported version.
// ParseGeneric intentionally accepts distribution suffixes used by managed
// Kubernetes products while still requiring numeric major/minor components.
func (g *Graph) SetKubernetesVersion(raw string) error {
	v, err := utilversion.ParseGeneric(raw)
	if err != nil {
		return fmt.Errorf("parse Kubernetes version %q: %w", raw, err)
	}
	if v.Major() == 0 {
		return fmt.Errorf("parse Kubernetes version %q: major version must be greater than zero", raw)
	}
	g.Cluster = ClusterInfo{
		KubernetesVersion: raw,
		Major:             v.Major(),
		Minor:             v.Minor(),
	}
	return nil
}

// HasKubernetesVersion reports whether a usable server major/minor was
// discovered. Kubernetes has never published a v0 cluster release, so zero is
// also a useful guard against placeholder client build versions in tests.
func (g *Graph) HasKubernetesVersion() bool {
	return g.Cluster.KubernetesVersion != "" && g.Cluster.Major > 0
}

func New(focus ResourceRef) *Graph {
	return &Graph{
		Focus: focus,
		nodes: map[string]*Node{},
	}
}

// BeginCollection records when a live collection began and the first observed
// revision of its focus resource.
func (g *Graph) BeginCollection(startedAt time.Time, focus metav1.Object) {
	revision := focusRevision(focus)
	g.collectionUID = focus.GetUID()
	g.collectionStart = revision
	g.Collection = &CollectionInfo{
		StartedAt:      startedAt.UTC(),
		FocusStart:     revision,
		FocusStability: FocusStabilityUnknown,
	}
}

// FinishCollection records the final focus revision and reports whether it is
// identical to the initial observation. A nil focus means the final read could
// not establish stability.
func (g *Graph) FinishCollection(completedAt time.Time, focus metav1.Object) FocusStability {
	if g.Collection == nil {
		g.Collection = &CollectionInfo{
			StartedAt: completedAt.UTC(), CompletedAt: completedAt.UTC(),
			FocusStability: FocusStabilityUnknown,
		}
		if focus != nil {
			revision := focusRevision(focus)
			g.Collection.FocusEnd = &revision
		}
		return FocusStabilityUnknown
	}
	g.Collection.CompletedAt = completedAt.UTC()
	if focus == nil {
		g.Collection.FocusStability = FocusStabilityUnknown
		return FocusStabilityUnknown
	}

	revision := focusRevision(focus)
	g.Collection.FocusEnd = &revision
	if focus.GetUID() == g.collectionUID && revision == g.collectionStart {
		g.Collection.FocusStability = FocusStabilityStable
		return FocusStabilityStable
	}
	g.Collection.FocusStability = FocusStabilityChanged
	return FocusStabilityChanged
}

func focusRevision(obj metav1.Object) FocusRevision {
	return FocusRevision{ResourceVersion: obj.GetResourceVersion(), Generation: obj.GetGeneration()}
}

// AddNode inserts or replaces a node. Adding a node with a non-nil object
// upgrades a previously-missing placeholder node.
func (g *Graph) AddNode(ref ResourceRef, obj any) *Node {
	key := ref.String()
	if existing, ok := g.nodes[key]; ok {
		if existing.Object == nil && obj != nil {
			existing.Object = obj
			existing.Unknown = false
		} else if obj == nil && existing.Unknown {
			// A definitive not-found result upgrades unknown to missing.
			existing.Unknown = false
		}
		return existing
	}
	n := &Node{Ref: ref, Object: obj}
	g.nodes[key] = n
	return n
}

// AddUnknownNode records a reference without claiming the target exists or is
// missing. It never downgrades an already-known node.
func (g *Graph) AddUnknownNode(ref ResourceRef) *Node {
	key := ref.String()
	if existing, ok := g.nodes[key]; ok {
		return existing
	}
	n := &Node{Ref: ref, Unknown: true}
	g.nodes[key] = n
	return n
}

func (g *Graph) AddEdge(from, to ResourceRef, t EdgeType) {
	for _, e := range g.edges {
		if e.From == from && e.To == to && e.Type == t {
			return
		}
	}
	g.edges = append(g.edges, Edge{From: from, To: to, Type: t})
}

func (g *Graph) Node(ref ResourceRef) (*Node, bool) {
	n, ok := g.nodes[ref.String()]
	return n, ok
}

func (g *Graph) Nodes() []*Node {
	out := make([]*Node, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.String() < out[j].Ref.String() })
	return out
}

// NodesOfKind returns all nodes of the given kind (e.g. "Pod").
func (g *Graph) NodesOfKind(kind string) []*Node {
	var out []*Node
	for _, n := range g.nodes {
		if n.Ref.Kind == kind {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.String() < out[j].Ref.String() })
	return out
}

func (g *Graph) Edges() []Edge {
	out := append([]Edge(nil), g.edges...)
	sortEdges(out)
	return out
}

// EdgesFrom returns edges of type t that originate at ref.
func (g *Graph) EdgesFrom(ref ResourceRef, t EdgeType) []Edge {
	var out []Edge
	for _, e := range g.edges {
		if e.Type == t && e.From == ref {
			out = append(out, e)
		}
	}
	sortEdges(out)
	return out
}

// HasEdge reports whether the exact directed, typed relationship exists.
func (g *Graph) HasEdge(from, to ResourceRef, t EdgeType) bool {
	for _, e := range g.edges {
		if e.From == from && e.To == to && e.Type == t {
			return true
		}
	}
	return false
}

// HasTypedPath follows an exact sequence of directed edge types. It is used
// by causal predicates such as Deployment --owns--> ReplicaSet --owns--> Pod.
func (g *Graph) HasTypedPath(from, to ResourceRef, path ...EdgeType) bool {
	if len(path) == 0 {
		return from == to
	}
	frontier := map[ResourceRef]struct{}{from: {}}
	for _, want := range path {
		next := map[ResourceRef]struct{}{}
		for ref := range frontier {
			for _, e := range g.edges {
				if e.From == ref && e.Type == want {
					next[e.To] = struct{}{}
				}
			}
		}
		frontier = next
		if len(frontier) == 0 {
			return false
		}
	}
	_, ok := frontier[to]
	return ok
}

// AddEvents attaches cluster events to the graph.
func (g *Graph) AddEvents(events ...corev1.Event) {
	g.events = append(g.events, events...)
}

// EventsFor returns events whose involved object matches ref. When the graph
// node for ref is a metav1.Object with a non-empty UID, only Events whose
// involvedObject.uid matches that UID exactly are returned. Empty or wrong
// involved-object UIDs are rejected so stale Events from a deleted same-name
// object cannot become rule evidence. Synthetic nodes without a UID keep the
// existing kind/name/namespace match for fixture graphs.
func (g *Graph) EventsFor(ref ResourceRef) []corev1.Event {
	requiredUID := objectUID(g, ref)
	var out []corev1.Event
	for _, ev := range g.events {
		if ev.InvolvedObject.Kind != ref.Kind ||
			ev.InvolvedObject.Name != ref.Name ||
			(ev.InvolvedObject.Namespace != ref.Namespace && ev.InvolvedObject.Namespace != "") {
			continue
		}
		if requiredUID != "" && (ev.InvolvedObject.UID == "" || ev.InvolvedObject.UID != requiredUID) {
			continue
		}
		out = append(out, ev)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := eventTime(out[i]), eventTime(out[j])
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		ki := out[i].Namespace + "/" + out[i].Name + "/" + out[i].Reason + "/" + out[i].Message
		kj := out[j].Namespace + "/" + out[j].Name + "/" + out[j].Reason + "/" + out[j].Message
		return ki < kj
	})
	return out
}

func objectUID(g *Graph, ref ResourceRef) types.UID {
	node, ok := g.Node(ref)
	if !ok || node.Object == nil {
		return ""
	}
	obj, ok := node.Object.(metav1.Object)
	if !ok {
		return ""
	}
	return obj.GetUID()
}

// AddCollectionIssue records an unavailable evidence source. Duplicate issues
// are suppressed so namespace-wide collection failures remain readable.
func (g *Graph) AddCollectionIssue(issue CollectionIssue) {
	for _, existing := range g.issues {
		if existing.Source == issue.Source && existing.Resource == issue.Resource && existing.Message == issue.Message {
			return
		}
	}
	g.issues = append(g.issues, issue)
}

func (g *Graph) CollectionIssues() []CollectionIssue {
	out := append([]CollectionIssue(nil), g.issues...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].Resource != out[j].Resource {
			return out[i].Resource.String() < out[j].Resource.String()
		}
		return out[i].Message < out[j].Message
	})
	return out
}

func (g *Graph) HasCollectionIssue(source string, ref ResourceRef) bool {
	for _, issue := range g.issues {
		if issue.Source == source && issue.Resource == ref {
			return true
		}
	}
	return false
}

func (g *Graph) HasCollectionSourceIssue(source string) bool {
	for _, issue := range g.issues {
		if issue.Source == source {
			return true
		}
	}
	return false
}

func (g *Graph) HasHealthBlockingIssue() bool {
	for _, issue := range g.issues {
		if issue.AffectsHealth {
			return true
		}
	}
	return false
}

func sortEdges(edges []Edge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From.String() < edges[j].From.String()
		}
		if edges[i].Type != edges[j].Type {
			return edges[i].Type < edges[j].Type
		}
		return edges[i].To.String() < edges[j].To.String()
	})
}

func eventTime(ev corev1.Event) time.Time {
	if ev.Series != nil && !ev.Series.LastObservedTime.IsZero() {
		return ev.Series.LastObservedTime.Time
	}
	if !ev.LastTimestamp.IsZero() {
		return ev.LastTimestamp.Time
	}
	if !ev.EventTime.IsZero() {
		return ev.EventTime.Time
	}
	if !ev.FirstTimestamp.IsZero() {
		return ev.FirstTimestamp.Time
	}
	return ev.CreationTimestamp.Time
}

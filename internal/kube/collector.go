// Package kube collects Kubernetes resources from a live cluster and builds
// the resource relationship graph that diagnostic rules operate on.
//
// Collection starts from a focus resource and retains only related objects in
// the graph. Some discovery calls are namespace-scoped because Kubernetes does
// not expose a direct relationship query; collection gaps are recorded on the
// graph instead of being treated as evidence that resources are absent.
package kube

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/leventozen/kdiag/internal/graph"
)

type Collector struct {
	client kubernetes.Interface
}

func NewCollector(client kubernetes.Interface) *Collector {
	return &Collector{client: client}
}

// CollectService builds a graph around a Service: selected Pods, their owner
// chain (ReplicaSet → Deployment), EndpointSlices, referenced ConfigMaps and
// Secret references, and related events. Secret payloads are never fetched.
func (c *Collector) CollectService(ctx context.Context, namespace, name string) (*graph.Graph, error) {
	svc, err := c.client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get service %s/%s: %w", namespace, name, err)
	}
	svcRef := graph.ResourceRef{Kind: "Service", Namespace: namespace, Name: name}
	g := graph.New(svcRef)
	g.AddNode(svcRef, svc)
	c.collectClusterInfo(ctx, g)

	// Pods selected by the Service.
	var selectedPods []corev1.Pod
	if len(svc.Spec.Selector) > 0 {
		podList, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: labels.Set(svc.Spec.Selector).AsSelector().String(),
		})
		if err != nil {
			g.AddCollectionIssue(graph.CollectionIssue{
				Source: graph.SourcePod, Resource: svcRef,
				Message: fmt.Sprintf("could not list Pods selected by Service: %v", err),
			})
		} else {
			selectedPods = podList.Items
		}
	}
	for i := range selectedPods {
		podRef := c.addPod(ctx, g, &selectedPods[i])
		g.AddEdge(svcRef, podRef, graph.EdgeSelects)
	}

	// EndpointSlices for the Service.
	slices, err := c.client.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: discoveryv1.LabelServiceName + "=" + name,
	})
	if err != nil {
		g.AddCollectionIssue(graph.CollectionIssue{
			Source: graph.SourceEndpointSlices, Resource: svcRef,
			Message: fmt.Sprintf("could not list EndpointSlices: %v", err), AffectsHealth: true,
		})
	} else {
		for i := range slices.Items {
			slice := &slices.Items[i]
			sliceRef := graph.ResourceRef{Kind: "EndpointSlice", Namespace: namespace, Name: slice.Name}
			g.AddNode(sliceRef, slice)
			g.AddEdge(svcRef, sliceRef, graph.EdgeRoutesTo)
		}
	}

	c.collectEvents(ctx, g, namespace)
	return g, nil
}

// CollectDeployment builds a graph around a Deployment: owned ReplicaSets
// and Pods, Services selecting those Pods, EndpointSlices, referenced
// ConfigMaps and Secret references, and related events. Secret payloads are
// never fetched.
func (c *Collector) CollectDeployment(ctx context.Context, namespace, name string) (*graph.Graph, error) {
	dep, err := c.client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
	}
	depRef := graph.ResourceRef{Kind: "Deployment", Namespace: namespace, Name: name}
	g := graph.New(depRef)
	g.AddNode(depRef, dep)
	c.collectClusterInfo(ctx, g)

	workloadSelector, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("parse deployment selector: %w", err)
	}

	// Owned ReplicaSets. The label selector narrows the API response; owner UID
	// remains the authoritative relationship check.
	ownedRS := map[string]*appsv1.ReplicaSet{}
	rsCollected := true
	rsList, err := c.client.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{LabelSelector: workloadSelector.String()})
	if err != nil {
		rsCollected = false
		g.AddCollectionIssue(graph.CollectionIssue{
			Source: graph.SourceReplicaSet, Resource: depRef,
			Message: fmt.Sprintf("could not list owned ReplicaSets: %v", err),
		})
	} else {
		for i := range rsList.Items {
			rs := &rsList.Items[i]
			if !isOwnedBy(rs.OwnerReferences, dep.UID) {
				continue
			}
			rsRef := graph.ResourceRef{Kind: "ReplicaSet", Namespace: namespace, Name: rs.Name}
			g.AddNode(rsRef, rs)
			g.AddEdge(depRef, rsRef, graph.EdgeOwns)
			ownedRS[string(rs.UID)] = rs
		}
	}

	// Pods owned by those ReplicaSets.
	var ownedPods []*corev1.Pod
	if rsCollected {
		podList, err := c.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: workloadSelector.String()})
		if err != nil {
			g.AddCollectionIssue(graph.CollectionIssue{
				Source: graph.SourcePod, Resource: depRef,
				Message: fmt.Sprintf("could not list Deployment Pods: %v", err),
			})
		} else {
			for i := range podList.Items {
				pod := &podList.Items[i]
				for _, or := range pod.OwnerReferences {
					if rs, ok := ownedRS[string(or.UID)]; ok {
						podRef := c.addPod(ctx, g, pod)
						g.AddEdge(graph.ResourceRef{Kind: "ReplicaSet", Namespace: namespace, Name: rs.Name}, podRef, graph.EdgeOwns)
						ownedPods = append(ownedPods, pod)
					}
				}
			}
		}
	}

	// Services selecting those Pods, plus their EndpointSlices.
	svcList := &corev1.ServiceList{}
	if len(ownedPods) > 0 {
		listed, err := c.client.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			g.AddCollectionIssue(graph.CollectionIssue{
				Source: graph.SourceService, Resource: depRef,
				Message: fmt.Sprintf("could not list Services selecting Deployment Pods: %v", err),
			})
		} else {
			svcList = listed
		}
	}
	for i := range svcList.Items {
		svc := &svcList.Items[i]
		if len(svc.Spec.Selector) == 0 {
			continue
		}
		sel := labels.Set(svc.Spec.Selector).AsSelector()
		matched := false
		svcRef := graph.ResourceRef{Kind: "Service", Namespace: namespace, Name: svc.Name}
		for _, pod := range ownedPods {
			if sel.Matches(labels.Set(pod.Labels)) {
				if !matched {
					g.AddNode(svcRef, svc)
					matched = true
				}
				g.AddEdge(svcRef, graph.ResourceRef{Kind: "Pod", Namespace: namespace, Name: pod.Name}, graph.EdgeSelects)
			}
		}
		if !matched {
			continue
		}
		slices, err := c.client.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: discoveryv1.LabelServiceName + "=" + svc.Name,
		})
		if err != nil {
			g.AddCollectionIssue(graph.CollectionIssue{
				Source: graph.SourceEndpointSlices, Resource: svcRef,
				Message: fmt.Sprintf("could not list EndpointSlices: %v", err),
			})
			continue
		}
		for j := range slices.Items {
			slice := &slices.Items[j]
			sliceRef := graph.ResourceRef{Kind: "EndpointSlice", Namespace: namespace, Name: slice.Name}
			g.AddNode(sliceRef, slice)
			g.AddEdge(svcRef, sliceRef, graph.EdgeRoutesTo)
		}
	}

	c.collectEvents(ctx, g, namespace)
	return g, nil
}

// addPod adds a Pod, its owner chain (ReplicaSet → Deployment), and its
// ConfigMap/Secret references to the graph. Secret payloads are never read.
func (c *Collector) addPod(ctx context.Context, g *graph.Graph, pod *corev1.Pod) graph.ResourceRef {
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: pod.Namespace, Name: pod.Name}
	g.AddNode(podRef, pod)

	// Owner chain.
	for _, or := range pod.OwnerReferences {
		if or.Kind != "ReplicaSet" {
			continue
		}
		rsRef := graph.ResourceRef{Kind: "ReplicaSet", Namespace: pod.Namespace, Name: or.Name}
		if _, exists := g.Node(rsRef); !exists {
			rs, err := c.client.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, or.Name, metav1.GetOptions{})
			if err != nil {
				g.AddUnknownNode(rsRef)
				g.AddCollectionIssue(graph.CollectionIssue{
					Source: graph.SourceReplicaSet, Resource: rsRef,
					Message: fmt.Sprintf("could not get owner ReplicaSet: %v", err),
				})
				g.AddEdge(rsRef, podRef, graph.EdgeOwns)
				continue
			}
			g.AddNode(rsRef, rs)
			for _, rsOwner := range rs.OwnerReferences {
				if rsOwner.Kind != "Deployment" {
					continue
				}
				depRef := graph.ResourceRef{Kind: "Deployment", Namespace: pod.Namespace, Name: rsOwner.Name}
				if _, exists := g.Node(depRef); !exists {
					if dep, err := c.client.AppsV1().Deployments(pod.Namespace).Get(ctx, rsOwner.Name, metav1.GetOptions{}); err == nil {
						g.AddNode(depRef, dep)
					} else {
						g.AddUnknownNode(depRef)
						g.AddCollectionIssue(graph.CollectionIssue{
							Source: graph.SourceDeployment, Resource: depRef,
							Message: fmt.Sprintf("could not get owner Deployment: %v", err),
						})
					}
				}
				g.AddEdge(depRef, rsRef, graph.EdgeOwns)
			}
		}
		g.AddEdge(rsRef, podRef, graph.EdgeOwns)
	}

	// ConfigMap references are checked for existence. Secret references remain
	// explicitly unknown because a typed GET would return their data payload.
	c.addConfigRefs(ctx, g, pod, podRef)

	// The Node the Pod is scheduled on: node conditions are evidence for
	// pressure/eviction diagnoses.
	if pod.Spec.NodeName != "" {
		nodeRef := graph.ResourceRef{Kind: "Node", Name: pod.Spec.NodeName}
		if _, exists := g.Node(nodeRef); !exists {
			if node, err := c.client.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{}); err == nil {
				g.AddNode(nodeRef, node)
			} else {
				g.AddUnknownNode(nodeRef)
				g.AddCollectionIssue(graph.CollectionIssue{
					Source: graph.SourceNode, Resource: nodeRef,
					Message: fmt.Sprintf("could not get scheduled Node: %v", err),
				})
			}
		}
		g.AddEdge(podRef, nodeRef, graph.EdgeScheduledOn)
	}
	return podRef
}

// CollectPod builds a graph around a single Pod: its owner chain, the Node
// it runs on, ConfigMap/Secret references, Services selecting it (plus
// their EndpointSlices), and related events.
func (c *Collector) CollectPod(ctx context.Context, namespace, name string) (*graph.Graph, error) {
	pod, err := c.client.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get pod %s/%s: %w", namespace, name, err)
	}
	podRef := graph.ResourceRef{Kind: "Pod", Namespace: namespace, Name: name}
	g := graph.New(podRef)
	c.addPod(ctx, g, pod)
	c.collectClusterInfo(ctx, g)

	// Services selecting this Pod, plus their EndpointSlices.
	svcList, err := c.client.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		g.AddCollectionIssue(graph.CollectionIssue{
			Source: graph.SourceService, Resource: podRef,
			Message: fmt.Sprintf("could not list Services selecting Pod: %v", err),
		})
		svcList = &corev1.ServiceList{}
	}
	for i := range svcList.Items {
		svc := &svcList.Items[i]
		if len(svc.Spec.Selector) == 0 ||
			!labels.Set(svc.Spec.Selector).AsSelector().Matches(labels.Set(pod.Labels)) {
			continue
		}
		svcRef := graph.ResourceRef{Kind: "Service", Namespace: namespace, Name: svc.Name}
		g.AddNode(svcRef, svc)
		g.AddEdge(svcRef, podRef, graph.EdgeSelects)

		slices, err := c.client.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: discoveryv1.LabelServiceName + "=" + svc.Name,
		})
		if err != nil {
			g.AddCollectionIssue(graph.CollectionIssue{
				Source: graph.SourceEndpointSlices, Resource: svcRef,
				Message: fmt.Sprintf("could not list EndpointSlices: %v", err),
			})
			continue
		}
		for j := range slices.Items {
			slice := &slices.Items[j]
			sliceRef := graph.ResourceRef{Kind: "EndpointSlice", Namespace: namespace, Name: slice.Name}
			g.AddNode(sliceRef, slice)
			g.AddEdge(svcRef, sliceRef, graph.EdgeRoutesTo)
		}
	}

	c.collectEvents(ctx, g, namespace)
	return g, nil
}

// collectClusterInfo records the API server version used to interpret rule
// semantics. Version discovery is evidence, not decoration: if it fails, the
// engine cannot prove that version-scoped rules apply and health becomes
// unknown unless another rule already proves a current focus problem.
func (c *Collector) collectClusterInfo(ctx context.Context, g *graph.Graph) {
	type versionResult struct {
		raw string
		err error
	}
	result := make(chan versionResult, 1)
	if err := ctx.Err(); err == nil {
		// DiscoveryInterface.ServerVersion has no context parameter. Bound the
		// collector's wait with ctx and use a buffered result so a transport that
		// finishes after cancellation cannot block this goroutine. The underlying
		// client request may still finish asynchronously.
		go func() {
			info, err := c.client.Discovery().ServerVersion()
			if err != nil {
				result <- versionResult{err: err}
				return
			}
			raw := info.GitVersion
			if raw == "" && info.Major != "" && info.Minor != "" {
				raw = "v" + info.Major + "." + info.Minor
			}
			result <- versionResult{raw: raw}
		}()
	}

	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case discovered := <-result:
		if discovered.err != nil {
			err = discovered.err
		} else {
			err = g.SetKubernetesVersion(discovered.raw)
		}
	}
	if err != nil {
		g.AddCollectionIssue(graph.CollectionIssue{
			Source: graph.SourceServerVersion, Resource: g.Focus,
			Message:       fmt.Sprintf("could not determine Kubernetes API server version: %v", err),
			AffectsHealth: true,
		})
	}
}

func (c *Collector) addConfigRefs(ctx context.Context, g *graph.Graph, pod *corev1.Pod, podRef graph.ResourceRef) {
	add := func(kind, name string, optional *bool) {
		if name == "" || (optional != nil && *optional) {
			return
		}
		ref := graph.ResourceRef{Kind: kind, Namespace: pod.Namespace, Name: name}
		if _, exists := g.Node(ref); exists {
			g.AddEdge(podRef, ref, graph.EdgeReferences)
			return
		}
		var obj any
		var err error
		switch kind {
		case "ConfigMap":
			obj, err = c.client.CoreV1().ConfigMaps(pod.Namespace).Get(ctx, name, metav1.GetOptions{})
		case "Secret":
			// A typed Secret GET returns the full data payload. Do not fetch
			// credentials merely to check existence; report this evidence as
			// unknown until a metadata-only lookup is implemented.
			g.AddUnknownNode(ref)
			g.AddEdge(podRef, ref, graph.EdgeReferences)
			g.AddCollectionIssue(graph.CollectionIssue{
				Source: graph.SourceSecret, Resource: ref,
				Message: "existence not checked because a typed GET would read Secret data",
			})
			return
		}
		if apierrors.IsNotFound(err) {
			obj = nil // placeholder: referenced but missing
		} else if err != nil {
			g.AddUnknownNode(ref)
			g.AddEdge(podRef, ref, graph.EdgeReferences)
			g.AddCollectionIssue(graph.CollectionIssue{
				Source: graph.SourceConfigMap, Resource: ref,
				Message: fmt.Sprintf("could not check referenced ConfigMap: %v", err),
			})
			return
		}
		g.AddNode(ref, obj)
		g.AddEdge(podRef, ref, graph.EdgeReferences)
	}

	containers := append(append([]corev1.Container{}, pod.Spec.InitContainers...), pod.Spec.Containers...)
	for _, ct := range containers {
		for _, ef := range ct.EnvFrom {
			if ef.ConfigMapRef != nil {
				add("ConfigMap", ef.ConfigMapRef.Name, ef.ConfigMapRef.Optional)
			}
			if ef.SecretRef != nil {
				add("Secret", ef.SecretRef.Name, ef.SecretRef.Optional)
			}
		}
		for _, env := range ct.Env {
			if env.ValueFrom == nil {
				continue
			}
			if env.ValueFrom.ConfigMapKeyRef != nil {
				add("ConfigMap", env.ValueFrom.ConfigMapKeyRef.Name, env.ValueFrom.ConfigMapKeyRef.Optional)
			}
			if env.ValueFrom.SecretKeyRef != nil {
				add("Secret", env.ValueFrom.SecretKeyRef.Name, env.ValueFrom.SecretKeyRef.Optional)
			}
		}
	}
	for _, v := range pod.Spec.Volumes {
		if v.ConfigMap != nil {
			add("ConfigMap", v.ConfigMap.Name, v.ConfigMap.Optional)
		}
		if v.Secret != nil {
			add("Secret", v.Secret.SecretName, v.Secret.Optional)
		}
		if v.Projected != nil {
			for _, source := range v.Projected.Sources {
				if source.ConfigMap != nil {
					add("ConfigMap", source.ConfigMap.Name, source.ConfigMap.Optional)
				}
				if source.Secret != nil {
					add("Secret", source.Secret.Name, source.Secret.Optional)
				}
			}
		}
	}
}

// collectEvents fetches Events only for related Pods whose current structured
// state is not Ready. Current rules use Pod Events as bounded supporting
// evidence; listing every Event in the namespace would expose unrelated
// workload data and create unnecessary volume.
func (c *Collector) collectEvents(ctx context.Context, g *graph.Graph, namespace string) {
	for _, node := range g.NodesOfKind("Pod") {
		pod, ok := node.Object.(*corev1.Pod)
		if !ok || !podNeedsEvents(pod) {
			continue
		}
		selector := fields.Set{
			"involvedObject.kind": "Pod",
			"involvedObject.name": pod.Name,
		}.AsSelector().String()
		evList, err := c.client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: selector})
		if err != nil {
			g.AddCollectionIssue(graph.CollectionIssue{
				Source: graph.SourceEvents, Resource: node.Ref,
				Message: fmt.Sprintf("could not list Pod events: %v", err),
			})
			continue
		}
		for _, ev := range evList.Items {
			if ev.InvolvedObject.Kind == "Pod" && ev.InvolvedObject.Name == pod.Name &&
				(ev.InvolvedObject.Namespace == namespace || ev.InvolvedObject.Namespace == "") {
				g.AddEvents(ev)
			}
		}
	}
}

func podNeedsEvents(pod *corev1.Pod) bool {
	if pod.Status.Phase == corev1.PodSucceeded {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status != corev1.ConditionTrue
		}
	}
	return true
}

func isOwnedBy(refs []metav1.OwnerReference, uid types.UID) bool {
	for _, r := range refs {
		if r.UID == uid {
			return true
		}
	}
	return false
}

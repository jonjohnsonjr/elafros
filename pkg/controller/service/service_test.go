/*
Copyright 2018 Google LLC
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package service

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/knative/serving/pkg/apis/istio/v1alpha2"
	"github.com/knative/serving/pkg/apis/serving/v1alpha1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/rest"
)

func getTestService() *v1alpha1.Service {
	return &v1alpha1.Service{
		ObjectMeta: metav1.ObjectMeta{
			SelfLink:  "/apis/serving/v1alpha1/namespaces/test/configurations/test-config",
			Name:      "test-config",
			Namespace: testNamespace,
		},
		Spec: v1alpha1.ConfigurationSpec{
			//TODO(grantr): This is a workaround for generation initialization
			Generation: 1,
			RevisionTemplate: v1alpha1.RevisionTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"test-label":                   "test",
						"example.com/namespaced-label": "test",
					},
					Annotations: map[string]string{
						"test-annotation-1": "foo",
						"test-annotation-2": "bar",
					},
				},
				Spec: v1alpha1.RevisionSpec{
					ServiceAccountName: "test-account",
					// corev1.Container has a lot of setting.  We try to pass many
					// of them here to verify that we pass through the settings to
					// the derived Revisions.
					Container: corev1.Container{
						Image:      "gcr.io/repo/image",
						Command:    []string{"echo"},
						Args:       []string{"hello", "world"},
						WorkingDir: "/tmp",
						Env: []corev1.EnvVar{{
							Name:  "EDITOR",
							Value: "emacs",
						}},
						LivenessProbe: &corev1.Probe{
							TimeoutSeconds: 42,
						},
						ReadinessProbe: &corev1.Probe{
							TimeoutSeconds: 43,
						},
						TerminationMessagePath: "/dev/null",
					},
				},
			},
		},
	}
}

func newTestController(t *testing.T, elaObjects ...runtime.Object) (
	kubeClient *fakekubeclientset.Clientset,
	elaClient *fakeclientset.Clientset,
	controller *Controller,
	kubeInformer kubeinformers.SharedInformerFactory,
	elaInformer informers.SharedInformerFactory) {

	// Create fake clients
	kubeClient = fakekubeclientset.NewSimpleClientset()
	// The ability to insert objects here is intended to work around the problem
	// with watches not firing in client-go 1.9. When we update to client-go 1.10
	// this can probably be removed.
	elaClient = fakeclientset.NewSimpleClientset(elaObjects...)

	// Create informer factories with fake clients. The second parameter sets the
	// resync period to zero, disabling it.
	kubeInformer = kubeinformers.NewSharedInformerFactory(kubeClient, 0)
	elaInformer = informers.NewSharedInformerFactory(elaClient, 0)

	controller = NewController(
		kubeClient,
		elaClient,
		kubeInformer,
		elaInformer,
		&rest.Config{},
		ctrl.Config{},
		zap.NewNop().Sugar(),
	).(*Controller)

	return
}

// TODO: Testing
// We should create a route.
// We should create a configuration.
// Do we care about anything else?

func TestCreateServiceCreatesStuff(t *testing.T) {
	kubeClient, elaClient, controller, _, elaInformer := newTestController(t)

	h := NewHooks()
	// Look for the events. Events are delivered asynchronously so we need to use
	// hooks here. Each hook tests for a specific event.
	h.OnCreate(&kubeClient.Fake, "events", ExpectNormalEventDelivery(t, `Created service "test-route-service"`))
	h.OnCreate(&kubeClient.Fake, "events", ExpectNormalEventDelivery(t, `Created Ingress "test-route-ingress"`))
	h.OnCreate(&kubeClient.Fake, "events", ExpectNormalEventDelivery(t, `Created Istio route rule "test-route-istio"`))
	h.OnCreate(&kubeClient.Fake, "events", ExpectNormalEventDelivery(t, `Updated status for route "test-route"`))

	// A standalone revision
	rev := getTestService("test-service")
	elaClient.ServingV1alpha1().Revisions(testNamespace).Create(rev)

	// A route targeting the revision
	route := getTestRouteWithTrafficTargets(
		[]v1alpha1.TrafficTarget{
			v1alpha1.TrafficTarget{
				RevisionName: "test-rev",
				Percent:      100,
			},
		},
	)
	elaClient.ServingV1alpha1().Routes(testNamespace).Create(route)
	// Since updateRouteEvent looks in the lister, we need to add it to the informer
	elaInformer.Serving().V1alpha1().Routes().Informer().GetIndexer().Add(route)

	controller.updateRouteEvent(KeyOrDie(route))

	// Look for the placeholder service.
	expectedServiceName := fmt.Sprintf("%s-service", route.Name)
	service, err := kubeClient.CoreV1().Services(testNamespace).Get(expectedServiceName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("error getting service: %v", err)
	}

	expectedPorts := []corev1.ServicePort{
		corev1.ServicePort{
			Name: "http",
			Port: 80,
		},
	}

	if diff := cmp.Diff(expectedPorts, service.Spec.Ports); diff != "" {
		t.Errorf("Unexpected service ports diff (-want +got): %v", diff)
	}

	// Look for the ingress.
	expectedIngressName := fmt.Sprintf("%s-ingress", route.Name)
	ingress, err := kubeClient.ExtensionsV1beta1().Ingresses(testNamespace).Get(expectedIngressName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("error getting ingress: %v", err)
	}

	expectedDomainPrefix := fmt.Sprintf("%s.%s.", route.Name, route.Namespace)
	expectedWildcardDomainPrefix := fmt.Sprintf("*.%s", expectedDomainPrefix)
	if !strings.HasPrefix(ingress.Spec.Rules[0].Host, expectedDomainPrefix) {
		t.Errorf("Ingress host %q does not have prefix %q", ingress.Spec.Rules[0].Host, expectedDomainPrefix)
	}
	if !strings.HasPrefix(ingress.Spec.Rules[1].Host, expectedWildcardDomainPrefix) {
		t.Errorf("Ingress host %q does not have prefix %q", ingress.Spec.Rules[1].Host, expectedWildcardDomainPrefix)
	}

	// Look for the route rule.
	routerule, err := elaClient.ConfigV1alpha2().RouteRules(testNamespace).Get(fmt.Sprintf("%s-istio", route.Name), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("error getting routerule: %v", err)
	}

	// Check labels
	expectedLabels := map[string]string{"route": route.Name}
	if diff := cmp.Diff(expectedLabels, routerule.Labels); diff != "" {
		t.Errorf("Unexpected label diff (-want +got): %v", diff)
	}

	// Check owner refs
	expectedRefs := []metav1.OwnerReference{
		metav1.OwnerReference{
			APIVersion: "serving.knative.dev/v1alpha1",
			Kind:       "Route",
			Name:       route.Name,
		},
	}

	if diff := cmp.Diff(expectedRefs, routerule.OwnerReferences, cmpopts.IgnoreFields(expectedRefs[0], "Controller", "BlockOwnerDeletion")); diff != "" {
		t.Errorf("Unexpected rule owner refs diff (-want +got): %v", diff)
	}

	expectedRouteSpec := v1alpha2.RouteRuleSpec{
		Destination: v1alpha2.IstioService{
			Name:      "test-route-service",
			Namespace: testNamespace,
		},
		Match: v1alpha2.Match{
			Request: v1alpha2.MatchRequest{
				Headers: v1alpha2.Headers{
					Authority: v1alpha2.MatchString{
						Regex: regexp.QuoteMeta(
							strings.Join([]string{route.Name, route.Namespace, defaultDomainSuffix}, "."),
						),
					},
				},
			},
		},
		Route: []v1alpha2.DestinationWeight{
			v1alpha2.DestinationWeight{
				Destination: v1alpha2.IstioService{
					Name:      "test-rev-service",
					Namespace: testNamespace,
				},
				Weight: 100,
			},
		},
	}

	if diff := cmp.Diff(expectedRouteSpec, routerule.Spec); diff != "" {
		t.Errorf("Unexpected rule spec diff (-want +got): %s", diff)
	}

	if err := h.WaitForHooks(time.Second * 3); err != nil {
		t.Error(err)
	}
}

func testNothing(t *testing.T) {
	// TODO(vaikas): Implement controller tests
	return
}

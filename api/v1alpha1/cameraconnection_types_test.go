package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestAddToScheme(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	kinds, _, err := scheme.ObjectKinds(&CameraConnection{})
	if err != nil {
		t.Fatalf("ObjectKinds() error = %v", err)
	}
	if len(kinds) != 1 || kinds[0].GroupVersion() != GroupVersion || kinds[0].Kind != "CameraConnection" {
		t.Fatalf("ObjectKinds() = %v", kinds)
	}
}

func TestCameraConnectionDeepCopy(t *testing.T) {
	t.Parallel()

	original := &CameraConnection{
		Status: CameraConnectionStatus{
			Conditions: []metav1.Condition{{
				Type:               "ResourcesReady",
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)),
				Reason:             "Provisioned",
			}},
		},
	}
	copy := original.DeepCopy()
	copy.Status.Conditions[0].Reason = "Changed"

	if original.Status.Conditions[0].Reason != "Provisioned" {
		t.Fatalf("DeepCopy() shared condition storage with original")
	}
}

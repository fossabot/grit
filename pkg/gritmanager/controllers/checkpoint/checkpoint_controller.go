// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

package checkpoint

import (
	"context"
	"reflect"
	"time"

	"github.com/kaito-project/grit/pkg/apis/v1alpha1"
	"github.com/kaito-project/grit/pkg/gritmanager/controllers/util"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	GritAgentLabel = "grit.dev/heler"
	GritAgentName  = "grit-agent"
)

var (
	podPredicate = predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			pod, ok := e.Object.(*corev1.Pod)
			if !ok {
				return false
			}

			if isGritAgentPodRunning(pod) {
				return true
			}
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			pod, ok := e.ObjectNew.(*corev1.Pod)
			if !ok {
				return false
			}

			if isGritAgentPodRunning(pod) {
				return true
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
)

func isGritAgentPodRunning(pod *corev1.Pod) bool {
	if pod.Labels[GritAgentLabel] == GritAgentName && pod.Status.Phase == corev1.PodRunning {
		return true
	}

	return false
}

type Controller struct {
	client.Client
	clock clock.Clock
}

func NewController(clk clock.Clock, kubeClient client.Client) *Controller {
	return &Controller{
		clock:  clk,
		Client: kubeClient,
	}
}

func (c *Controller) Reconcile(ctx context.Context, ckpt *v1alpha1.Checkpoint) (reconcile.Result, error) {
	ctx = util.WithControllerName(ctx, "checkpoint.lifecycle")

	updatedCkpt := ckpt.DeepCopy()
	if err := c.checkpointStateMachine(ctx, updatedCkpt); err != nil {
		return reconcile.Result{}, err
	}

	if !reflect.DeepEqual(ckpt, updatedCkpt) {
		return reconcile.Result{}, c.Status().Update(ctx, updatedCkpt)
	}
	return reconcile.Result{}, nil
}

func (c *Controller) checkpointStateMachine(ctx context.Context, ckpt *v1alpha1.Checkpoint) error {
	switch ckpt.Status.Phase {
	case v1alpha1.CheckpointPending:

	case v1alpha1.Checkpointing:

	case v1alpha1.Checkpointed:

	default:
		// checkpoint resouce is created, configure podSpecHash field and set state to CheckpointPending
		var pod corev1.Pod
		if err := c.Get(ctx, client.ObjectKey{Namespace: ckpt.Namespace, Name: ckpt.Spec.PodName}, &pod); err != nil {
			if apierrors.IsNotFound(err) {
				ckpt.Status.Phase = v1alpha1.CheckpointFailed

				return nil
			}
			return err
		}

	}

	return nil
}

func (c *Controller) updateCondition(ckpt *v1alpha1.Checkpoint, status metav1.ConditionStatus, conditionType, reason, message string) {
	newCondition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.NewTime(c.clock.Now()),
	}

	for i, cond := range ckpt.Status.Conditions {
		if cond.Type == conditionType {
			ckpt.Status.Conditions[i] = newCondition
			return
		}
	}

	ckpt.Status.Conditions = append(ckpt.Status.Conditions, newCondition)
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("checkpoint.lifecycle").
		For(&v1alpha1.Checkpoint{}).
		Watches(&corev1.Pod{}, &handler.EnqueueRequestForObject{}, builder.WithPredicates(podPredicate)).
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedMaxOfRateLimiter(
				workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](time.Second, 300*time.Second),
				&workqueue.TypedBucketRateLimiter[reconcile.Request]{Limiter: rate.NewLimiter(rate.Limit(10), 100)},
			),
			MaxConcurrentReconciles: 5,
		}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}

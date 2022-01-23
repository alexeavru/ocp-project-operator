package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NamespaceControllerReconciler reconciles a NamespaceController object
type NamespaceControllerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const namespacedockerSecret = "namespace-controller"
const dockerSecretName = "docker-gpn.nexign.com"

//+kubebuilder:rbac:groups=apps.alexeav.ru,resources=namespacecontrollers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps.alexeav.ru,resources=namespacecontrollers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=apps.alexeav.ru,resources=namespacecontrollers/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch;create;update;patch;delete

func (r *NamespaceControllerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// Fetch the Namespace Instance
	namespaceInstance := &corev1.Namespace{}
	err := r.Client.Get(context.TODO(), req.NamespacedName, namespaceInstance)
	if err != nil {
		if apiErrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	if namespaceInstance.Status.Phase == corev1.NamespaceActive {
		var configs, secretsName []string
		// logger.Info(fmt.Sprintf("Handle event for namespace: %s", namespaceName))
		configs, err = r.getDockerSecret(configs, dockerSecretName, namespacedockerSecret)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(configs) > 0 {
			// Create secret
			r.addDockerSecret(configs, dockerSecretName, namespaceInstance)
			// Update "default" serviceaccount
			secretsName, _ = r.getPullSecretsFromServiceaccount(namespaceInstance.Name)
			// SKIP If pullSecret eexist
			if !contains(secretsName, dockerSecretName) {
				secretsName = append(secretsName, dockerSecretName)
				r.patchDefaultServiceaccount(namespaceInstance.Name, secretsName)
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceControllerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Complete(r)
}

// Get docker Secret from namespace set in namespacedockerSecret
func (r *NamespaceControllerReconciler) getDockerSecret(configs []string, secretName string, namespace string) ([]string, error) {
	logger := log.Log.WithValues()
	secret := &corev1.Secret{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: secretName, Namespace: namespace}, secret)

	if err != nil {
		logger.Info(fmt.Sprintf("Secret name: %s not found in Namespace: %s", secretName, namespace))
		return configs, nil
	}

	for _, value := range secret.Data {
		// logger.V(1).Info(fmt.Sprintf("Add %s from Secret %s in namespace %s", key, secretName, namespace))
		stringData := string(value)
		configs = append(configs, stringData)
	}

	return configs, nil
}

// Add docker Secret
func (r *NamespaceControllerReconciler) addDockerSecret(configs []string, secretName string, namespace *corev1.Namespace) (ctrl.Result, error) {
	logger := log.Log.WithValues()
	secret := &corev1.Secret{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: secretName, Namespace: namespace.Name}, secret)

	if err != nil {
		sec := r.secretForApp(configs[0], secretName, namespace.Name)
		err = r.Client.Create(context.TODO(), sec)

		if err != nil {
			logger.Info(fmt.Sprintf("Error create Secret in namespace %s", namespace.Name))
		}
		logger.Info(fmt.Sprintf("Creating secret: %s in namespace %s", secretName, namespace.Name))
		return reconcile.Result{}, err
	} else {
		logger.V(1).Info(fmt.Sprintf("SKIP: Secret %s present in namespace %s", secretName, namespace.Name))
	}

	return ctrl.Result{}, nil
}

func (r *NamespaceControllerReconciler) secretForApp(secretData string, secretName string, namespace string) *corev1.Secret {

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Type:       "kubernetes.io/dockerconfigjson",
		StringData: map[string]string{".dockerconfigjson": secretData},
	}

	return sec
}

// Add pullsecret in default ServiceAccount
func (r *NamespaceControllerReconciler) patchDefaultServiceaccount(namespace string, secretName []string) (ctrl.Result, error) {
	logger := log.Log.WithValues()

	found := &corev1.ServiceAccount{}

	// loop witing create DEFAULT serviceaccount
	for i := 0; i < 3; i++ {
		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: "default", Namespace: namespace}, found)
		if err != nil {
			time.Sleep(2 * time.Second)
		} else {
			found.ImagePullSecrets = make([]corev1.LocalObjectReference, len(secretName))
			for i := 0; i < len(secretName); i++ {
				found.ImagePullSecrets[i].Name = secretName[i]
			}

			if err = r.Client.Update(context.TODO(), found); err != nil {
				return reconcile.Result{}, err
			} else {
				logger.Info(fmt.Sprintf("Add pullSecret to ServiceAccount DEFAULT in Namespace: %s", namespace))
				return reconcile.Result{}, err
			}

		}
	}

	return ctrl.Result{}, nil
}

// Add pullsecret in default ServiceAccount
func (r *NamespaceControllerReconciler) getPullSecretsFromServiceaccount(namespace string) ([]string, error) {

	var pullSecretsList []string

	found := &corev1.ServiceAccount{}
	err := r.Client.Get(context.TODO(), types.NamespacedName{Name: "default", Namespace: namespace}, found)
	if err != nil {
		return nil, err
	}

	for _, s := range found.ImagePullSecrets {
		pullSecretsList = append(pullSecretsList, s.Name)
	}

	return pullSecretsList, nil
}

// Function checking if element exist inarray
func contains(arr []string, str string) bool {
	for _, a := range arr {
		if a == str {
			return true
		}
	}
	return false
}

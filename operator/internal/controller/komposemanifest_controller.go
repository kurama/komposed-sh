package controller

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	komposev1alpha1 "github.com/kurama/komposed-sh/api/v1alpha1"
)

// KomposeManifestReconciler reconciles a KomposeManifest object
type KomposeManifestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=cache.example.com,resources=memcacheds,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cache.example.com,resources=memcacheds/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cache.example.com,resources=memcacheds/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;
func (r *KomposeManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the KomposeManifest instance
	var manifest komposev1alpha1.KomposeManifest
	if err := r.Get(ctx, req.NamespacedName, &manifest); err != nil {
		log.Error(err, "unable to fetch KomposeManifest")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Create a temporary directory for Kompose
	tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("kompose-%s", time.Now().Format("20060102150405")))
	if err := os.MkdirAll(tmpDir, os.ModePerm); err != nil {
		return ctrl.Result{}, err
	}
	defer os.RemoveAll(tmpDir)

	// Write docker-compose.yaml into tmpDir
	composePath := filepath.Join(tmpDir, "docker-compose.yaml")
	if err := os.WriteFile(composePath, []byte(manifest.Spec.DockerCompose), 0644); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to write compose file: %w", err)
	}

	// Kompose convert
	convertCmd := exec.Command("kompose", "convert", "-f", composePath, "-o", tmpDir)
	var convertOut, convertErr bytes.Buffer
	convertCmd.Stdout = &convertOut
	convertCmd.Stderr = &convertErr
	if err := convertCmd.Run(); err != nil {
		log.Error(err, "kompose convert failed", "stdout", convertOut.String(), "stderr", convertErr.String())
		return ctrl.Result{}, fmt.Errorf("kompose convert failed: %v - %s", err, convertErr.String())
	}

	// Read and apply the generated YAML files
	files, err := os.ReadDir(tmpDir)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to read tmpDir: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if name == "docker-compose.yaml" || !strings.HasSuffix(name, ".yaml") {
			continue
		}

		filePath := filepath.Join(tmpDir, name)
		content, err := os.ReadFile(filePath)
		if err != nil {
			log.Error(err, "failed to read file", "file", name)
			return ctrl.Result{}, fmt.Errorf("failed to read file %s: %w", name, err)
		}

		// Decode the YAML content into unstructured objects
		decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(content), 4096)
		for {
			var u unstructured.Unstructured
			if err := decoder.Decode(&u); err != nil {
				break
			}

			// Apply the unstructured object
			if err := r.Apply(ctx, &u, manifest.Namespace); err != nil {
				log.Error(err, "failed to apply resource", "file", name)
				return ctrl.Result{}, fmt.Errorf("failed to apply resource from file %s: %w", name, err)
			}
		}
	}

	log.Info("Successfully reconciled KomposeManifest", "name", manifest.Name)
	return ctrl.Result{}, nil
}

// Apply applies a Kubernetes resource using the controller-runtime client
func (r *KomposeManifestReconciler) Apply(ctx context.Context, obj *unstructured.Unstructured, namespace string) error {
	// Set the namespace for the object
	obj.SetNamespace(namespace)

	// Get the current state of the resource
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(obj.GroupVersionKind())
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), current)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get resource: %w", err)
		}
		// Resource does not exist, create it
		if err := r.Create(ctx, obj); err != nil {
			return fmt.Errorf("failed to create resource: %w", err)
		}
		return nil
	}

	// Resource exists, update it
	obj.SetResourceVersion(current.GetResourceVersion())
	if err := r.Update(ctx, obj); err != nil {
		return fmt.Errorf("failed to update resource: %w", err)
	}

	return nil
}

func (r *KomposeManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&komposev1alpha1.KomposeManifest{}).
		Complete(r)
}

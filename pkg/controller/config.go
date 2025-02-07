package controller

import (
	pcm "github.com/coreos/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"
	cs "github.com/kubedb/apimachinery/client/clientset/versioned"
	amc "github.com/kubedb/apimachinery/pkg/controller"
	"github.com/kubedb/apimachinery/pkg/controller/dormantdatabase"
	"github.com/kubedb/apimachinery/pkg/controller/restoresession"
	snapc "github.com/kubedb/apimachinery/pkg/controller/snapshot"
	"github.com/kubedb/apimachinery/pkg/eventer"
	crd_cs "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	reg_util "kmodules.xyz/client-go/admissionregistration/v1beta1"
	"kmodules.xyz/client-go/discovery"
	appcat_cs "kmodules.xyz/custom-resources/client/clientset/versioned/typed/appcatalog/v1alpha1"
	scs "stash.appscode.dev/stash/client/clientset/versioned"
)

const (
	mutatingWebhookConfig   = "mutators.kubedb.com"
	validatingWebhookConfig = "validators.kubedb.com"
)

type OperatorConfig struct {
	amc.Config

	ClientConfig     *rest.Config
	KubeClient       kubernetes.Interface
	APIExtKubeClient crd_cs.ApiextensionsV1beta1Interface
	DBClient         cs.Interface
	StashClient      scs.Interface
	AppCatalogClient appcat_cs.AppcatalogV1alpha1Interface
	DynamicClient    dynamic.Interface
	PromClient       pcm.MonitoringV1Interface
	CronController   snapc.CronControllerInterface
}

func NewOperatorConfig(clientConfig *rest.Config) *OperatorConfig {
	return &OperatorConfig{
		ClientConfig: clientConfig,
	}
}

func (c *OperatorConfig) New() (*Controller, error) {
	if err := discovery.IsDefaultSupportedVersion(c.KubeClient); err != nil {
		return nil, err
	}
	recorder := eventer.NewEventRecorder(c.KubeClient, "Postgres operator")
	ctrl := New(
		c.ClientConfig,
		c.KubeClient,
		c.APIExtKubeClient,
		c.DBClient,
		c.StashClient,
		c.DynamicClient,
		c.AppCatalogClient,
		c.PromClient,
		c.CronController,
		c.Config,
		recorder,
	)

	tweakListOptions := func(options *metav1.ListOptions) {
		options.LabelSelector = ctrl.selector.String()
	}

	// Initialize Job and Snapshot Informer. Later EventHandler will be added to these informers.
	ctrl.DrmnInformer = dormantdatabase.NewController(ctrl.Controller, ctrl, ctrl.Config, tweakListOptions, recorder).InitInformer()
	ctrl.SnapInformer, ctrl.JobInformer = snapc.NewController(ctrl.Controller, ctrl, ctrl.Config, tweakListOptions, recorder).InitInformer()
	ctrl.RSInformer = restoresession.NewController(ctrl.Controller, ctrl, ctrl.Config, tweakListOptions, recorder).InitInformer()

	if err := ctrl.EnsureCustomResourceDefinitions(); err != nil {
		return nil, err
	}
	if c.EnableMutatingWebhook {
		if err := reg_util.UpdateMutatingWebhookCABundle(c.ClientConfig, mutatingWebhookConfig); err != nil {
			return nil, err
		}
	}
	if c.EnableValidatingWebhook {
		if err := reg_util.UpdateValidatingWebhookCABundle(c.ClientConfig, validatingWebhookConfig); err != nil {
			return nil, err
		}
	}

	if err := ctrl.Init(); err != nil {
		return nil, err
	}

	return ctrl, nil
}

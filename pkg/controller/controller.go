package controller

import (
	"reflect"
	"time"

	"github.com/appscode/go/hold"
	"github.com/appscode/log"
	pcm "github.com/coreos/prometheus-operator/pkg/client/monitoring/v1alpha1"
	tapi "github.com/k8sdb/apimachinery/api"
	tcs "github.com/k8sdb/apimachinery/client/clientset"
	amc "github.com/k8sdb/apimachinery/pkg/controller"
	"github.com/k8sdb/apimachinery/pkg/eventer"
	kapi "k8s.io/kubernetes/pkg/api"
	k8serr "k8s.io/kubernetes/pkg/api/errors"
	"k8s.io/kubernetes/pkg/api/unversioned"
	"k8s.io/kubernetes/pkg/apis/extensions"
	"k8s.io/kubernetes/pkg/client/cache"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/client/record"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/util/wait"
	"k8s.io/kubernetes/pkg/watch"
)

type Controller struct {
	*amc.Controller
	// Cron Controller
	cronController amc.CronControllerInterface
	// Prometheus client
	PromClient *pcm.MonitoringV1alpha1Client
	// Event Recorder
	eventRecorder record.EventRecorder
	// Tag of postgres util
	postgresUtilTag string
	// Governing service
	governingService string
	// sync time to sync the list.
	syncPeriod time.Duration
}

var _ amc.Snapshotter = &Controller{}
var _ amc.Deleter = &Controller{}

func New(
	client clientset.Interface,
	extClient tcs.ExtensionInterface,
	promClient *pcm.MonitoringV1alpha1Client,
	postgresUtilTag,
	governingService string,
) *Controller {
	return &Controller{
		Controller: &amc.Controller{
			Client:    client,
			ExtClient: extClient,
		},
		cronController:   amc.NewCronController(client, extClient),
		PromClient:       promClient,
		eventRecorder:    eventer.NewEventRecorder(client, "Postgres Controller"),
		postgresUtilTag:  postgresUtilTag,
		governingService: governingService,
		syncPeriod:       time.Minute * 2,
	}
}

// Blocks caller. Intended to be called as a Go routine.
func (c *Controller) RunAndHold() {
	// Ensure Postgres TPR
	c.ensureThirdPartyResource()

	// Start Cron
	c.cronController.StartCron()
	// Stop Cron
	defer c.cronController.StopCron()

	// Watch Postgres TPR objects
	go c.watchPostgres()
	// Watch Snapshot with labelSelector only for Postgres
	go c.watchSnapshot()
	// Watch DormantDatabase with labelSelector only for Postgres
	go c.watchDormantDatabase()
	// hold
	hold.Hold()
}

func (c *Controller) watchPostgres() {
	lw := &cache.ListWatch{
		ListFunc: func(opts kapi.ListOptions) (runtime.Object, error) {
			return c.ExtClient.Postgreses(kapi.NamespaceAll).List(kapi.ListOptions{})
		},
		WatchFunc: func(options kapi.ListOptions) (watch.Interface, error) {
			return c.ExtClient.Postgreses(kapi.NamespaceAll).Watch(kapi.ListOptions{})
		},
	}

	_, cacheController := cache.NewInformer(
		lw,
		&tapi.Postgres{},
		c.syncPeriod,
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				postgres := obj.(*tapi.Postgres)
				if postgres.Status.CreationTime == nil {
					if err := c.create(postgres); err != nil {
						log.Errorln(err)
					}
				}
			},
			DeleteFunc: func(obj interface{}) {
				if err := c.pause(obj.(*tapi.Postgres)); err != nil {
					log.Errorln(err)
				}
			},
			UpdateFunc: func(old, new interface{}) {
				oldObj, ok := old.(*tapi.Postgres)
				if !ok {
					return
				}
				newObj, ok := new.(*tapi.Postgres)
				if !ok {
					return
				}
				if !reflect.DeepEqual(oldObj.Spec, newObj.Spec) {
					if err := c.update(oldObj, newObj); err != nil {
						log.Errorln(err)
					}
				}
			},
		},
	)
	cacheController.Run(wait.NeverStop)
}

func (c *Controller) watchSnapshot() {
	labelMap := map[string]string{
		amc.LabelDatabaseKind: tapi.ResourceKindPostgres,
	}
	// Watch with label selector
	lw := &cache.ListWatch{
		ListFunc: func(opts kapi.ListOptions) (runtime.Object, error) {
			return c.ExtClient.Snapshots(kapi.NamespaceAll).List(
				kapi.ListOptions{
					LabelSelector: labels.SelectorFromSet(labels.Set(labelMap)),
				})
		},
		WatchFunc: func(options kapi.ListOptions) (watch.Interface, error) {
			return c.ExtClient.Snapshots(kapi.NamespaceAll).Watch(
				kapi.ListOptions{
					LabelSelector: labels.SelectorFromSet(labels.Set(labelMap)),
				})
		},
	}

	amc.NewSnapshotController(c.Client, c.ExtClient, c, lw, c.syncPeriod).Run()
}

func (c *Controller) watchDormantDatabase() {
	labelMap := map[string]string{
		amc.LabelDatabaseKind: tapi.ResourceKindPostgres,
	}
	// Watch with label selector
	lw := &cache.ListWatch{
		ListFunc: func(opts kapi.ListOptions) (runtime.Object, error) {
			return c.ExtClient.DormantDatabases(kapi.NamespaceAll).List(
				kapi.ListOptions{
					LabelSelector: labels.SelectorFromSet(labels.Set(labelMap)),
				})
		},
		WatchFunc: func(options kapi.ListOptions) (watch.Interface, error) {
			return c.ExtClient.DormantDatabases(kapi.NamespaceAll).Watch(
				kapi.ListOptions{
					LabelSelector: labels.SelectorFromSet(labels.Set(labelMap)),
				})
		},
	}

	amc.NewDormantDbController(c.Client, c.ExtClient, c, lw, c.syncPeriod).Run()
}

func (c *Controller) ensureThirdPartyResource() {
	log.Infoln("Ensuring ThirdPartyResource...")

	resourceName := tapi.ResourceNamePostgres + "." + tapi.V1beta1SchemeGroupVersion.Group
	if _, err := c.Client.Extensions().ThirdPartyResources().Get(resourceName); err != nil {
		if !k8serr.IsNotFound(err) {
			log.Fatalln(err)
		}
	} else {
		return
	}

	thirdPartyResource := &extensions.ThirdPartyResource{
		TypeMeta: unversioned.TypeMeta{
			APIVersion: "extensions/v1beta1",
			Kind:       "ThirdPartyResource",
		},
		ObjectMeta: kapi.ObjectMeta{
			Name: resourceName,
		},
		Versions: []extensions.APIVersion{
			{
				Name: tapi.V1beta1SchemeGroupVersion.Version,
			},
		},
	}

	if _, err := c.Client.Extensions().ThirdPartyResources().Create(thirdPartyResource); err != nil {
		log.Fatalln(err)
	}
}

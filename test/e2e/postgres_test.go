package e2e_test

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/appscode/go/log"
	"github.com/appscode/go/types"
	core_util "github.com/appscode/kutil/core/v1"
	meta_util "github.com/appscode/kutil/meta"
	catalog "github.com/kubedb/apimachinery/apis/catalog/v1alpha1"
	api "github.com/kubedb/apimachinery/apis/kubedb/v1alpha1"
	"github.com/kubedb/apimachinery/client/clientset/versioned/typed/kubedb/v1alpha1/util"
	"github.com/kubedb/postgres/test/e2e/framework"
	"github.com/kubedb/postgres/test/e2e/matcher"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	core "k8s.io/api/core/v1"
	kerr "k8s.io/apimachinery/pkg/api/errors"
	store "kmodules.xyz/objectstore-api/api/v1"
)

const (
	S3_BUCKET_NAME          = "S3_BUCKET_NAME"
	GCS_BUCKET_NAME         = "GCS_BUCKET_NAME"
	AZURE_CONTAINER_NAME    = "AZURE_CONTAINER_NAME"
	SWIFT_CONTAINER_NAME    = "SWIFT_CONTAINER_NAME"
	POSTGRES_DB             = "POSTGRES_DB"
	POSTGRES_PASSWORD       = "POSTGRES_PASSWORD"
	PGDATA                  = "PGDATA"
	POSTGRES_USER           = "POSTGRES_USER"
	POSTGRES_INITDB_ARGS    = "POSTGRES_INITDB_ARGS"
	POSTGRES_INITDB_WALDIR  = "POSTGRES_INITDB_WALDIR"
	POSTGRES_INITDB_XLOGDIR = "POSTGRES_INITDB_XLOGDIR"
)

var _ = Describe("Postgres", func() {
	var (
		err                      error
		f                        *framework.Invocation
		postgres                 *api.Postgres
		garbagePostgres          *api.PostgresList
		postgresVersion          *catalog.PostgresVersion
		snapshot                 *api.Snapshot
		secret                   *core.Secret
		skipMessage              string
		skipSnapshotDataChecking bool
		skipWalDataChecking      bool
		dbName                   string
		dbUser                   string
	)

	BeforeEach(func() {
		f = root.Invoke()
		postgres = f.Postgres()
		postgresVersion = f.PostgresVersion()
		garbagePostgres = new(api.PostgresList)
		snapshot = f.Snapshot()
		secret = nil
		skipMessage = ""
		skipSnapshotDataChecking = true
		skipWalDataChecking = true
		dbName = "postgres"
		dbUser = "postgres"
	})

	var createAndWaitForRunning = func() {

		By("Ensuring PostgresVersion crd: " + postgresVersion.Spec.DB.Image)
		err = f.CreatePostgresVersion(postgresVersion)
		Expect(err).NotTo(HaveOccurred())

		By("Creating Postgres: " + postgres.Name)
		err = f.CreatePostgres(postgres)
		Expect(err).NotTo(HaveOccurred())

		By("Wait for Running postgres")
		f.EventuallyPostgresRunning(postgres.ObjectMeta).Should(BeTrue())

		By("Waiting for database to be ready")
		f.EventuallyPingDatabase(
			postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
			Should(BeTrue())

		By("Wait for AppBinding to create")
		f.EventuallyAppBinding(postgres.ObjectMeta).Should(BeTrue())

		By("Check valid AppBinding Specs")
		err := f.CheckAppBindingSpec(postgres.ObjectMeta)
		Expect(err).NotTo(HaveOccurred())
	}

	var pauseAndResumeAgain = func() {
		By("Delete postgres")
		err = f.DeletePostgres(postgres.ObjectMeta)
		Expect(err).NotTo(HaveOccurred())

		By("Wait for postgres to be paused")
		f.EventuallyDormantDatabaseStatus(postgres.ObjectMeta).Should(matcher.HavePaused())

		// Create Postgres object again to resume it
		By("Create Postgres: " + postgres.Name)
		err = f.CreatePostgres(postgres)
		Expect(err).NotTo(HaveOccurred())

		By("Wait for DormantDatabase to be deleted")
		f.EventuallyDormantDatabase(postgres.ObjectMeta).Should(BeFalse())

		By("Wait for Running postgres")
		f.EventuallyPostgresRunning(postgres.ObjectMeta).Should(BeTrue())
	}

	var testGeneralBehaviour = func() {
		if skipMessage != "" {
			Skip(skipMessage)
		}
		// Create Postgres
		createAndWaitForRunning()

		By("Creating Schema")
		f.EventuallyCreateSchema(postgres.ObjectMeta, dbName, dbUser).
			Should(BeTrue())

		By("Creating Table")
		f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 3).
			Should(BeTrue())

		By("Checking Table")
		f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
			Should(Equal(3))

		pauseAndResumeAgain()

		By("Checking Table")
		f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
			Should(Equal(3))
	}

	var shouldTakeSnapshot = func() {
		// Create and wait for running Postgres
		createAndWaitForRunning()

		By("Create Secret")
		err := f.CreateSecret(secret)
		Expect(err).NotTo(HaveOccurred())

		By("Create Snapshot")
		err = f.CreateSnapshot(snapshot)
		Expect(err).NotTo(HaveOccurred())

		By("Check for Succeeded snapshot")
		f.EventuallySnapshotPhase(snapshot.ObjectMeta).
			Should(Equal(api.SnapshotPhaseSucceeded))

		if !skipSnapshotDataChecking {
			By("Check for snapshot data")
			f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
		}
	}

	var shouldInsertDataAndTakeSnapshot = func() {
		// Create and wait for running Postgres
		createAndWaitForRunning()

		By("Creating Schema")
		f.EventuallyCreateSchema(postgres.ObjectMeta, dbName, dbUser).
			Should(BeTrue())

		By("Creating Table")
		f.EventuallyCreateTable(
			postgres.ObjectMeta, dbName, dbUser, 3).
			Should(BeTrue())

		By("Checking Table")
		f.EventuallyCountTableFromPrimary(
			postgres.ObjectMeta, dbName, dbUser).
			Should(Equal(3))

		By("Create Secret")
		err = f.CreateSecret(secret)
		Expect(err).NotTo(HaveOccurred())

		By("Create Snapshot")
		err = f.CreateSnapshot(snapshot)
		Expect(err).NotTo(HaveOccurred())

		By("Check for Succeeded snapshot")
		f.EventuallySnapshotPhase(snapshot.ObjectMeta).Should(Equal(api.SnapshotPhaseSucceeded))

		if !skipSnapshotDataChecking {
			By("Check for snapshot data")
			f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
		}
	}

	var deleteTestResource = func() {
		if postgres == nil {
			Skip("Skipping")
		}

		By("Check if Postgres " + postgres.Name + " exists.")
		pg, err := f.GetPostgres(postgres.ObjectMeta)
		if err != nil {
			if kerr.IsNotFound(err) {
				// Postgres was not created. Hence, rest of cleanup is not necessary.
				return
			}
			Expect(err).NotTo(HaveOccurred())
		}

		By("Delete postgres: " + postgres.Name)
		err = f.DeletePostgres(postgres.ObjectMeta)
		if err != nil {
			if kerr.IsNotFound(err) {
				// Postgres was not created. Hence, rest of cleanup is not necessary.
				log.Infof("Skipping rest of cleanup. Reason: Postgres %s is not found.", postgres.Name)
				return
			}
			Expect(err).NotTo(HaveOccurred())
		}

		if pg.Spec.TerminationPolicy == api.TerminationPolicyPause {

			By("Wait for postgres to be paused")
			f.EventuallyDormantDatabaseStatus(postgres.ObjectMeta).Should(matcher.HavePaused())

			By("Set DormantDatabase Spec.WipeOut to true")
			_, err := f.PatchDormantDatabase(postgres.ObjectMeta, func(in *api.DormantDatabase) *api.DormantDatabase {
				in.Spec.WipeOut = true
				return in
			})
			Expect(err).NotTo(HaveOccurred())

			By("Delete Dormant Database")
			err = f.DeleteDormantDatabase(postgres.ObjectMeta)
			if !kerr.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}

		}

		By("Wait for postgres resources to be wipedOut")
		f.EventuallyWipedOut(postgres.ObjectMeta).Should(Succeed())

		if postgres.Spec.Archiver != nil && !skipWalDataChecking {
			By("Checking wal data has been removed")
			f.EventuallyWalDataFound(postgres).Should(BeFalse())
		}
	}

	AfterEach(func() {
		// Delete test resource
		deleteTestResource()

		for _, pg := range garbagePostgres.Items {
			*postgres = pg
			// Delete test resource
			deleteTestResource()
		}

		if !skipSnapshotDataChecking {
			By("Check for snapshot data")
			f.EventuallySnapshotDataFound(snapshot).Should(BeFalse())
		}

		if secret != nil {
			err := f.DeleteSecret(secret.ObjectMeta)
			Expect(err).NotTo(HaveOccurred())
		}

		By("Deleting PostgresVersion crd")
		err = f.DeletePostgresVersion(postgresVersion.ObjectMeta)
		if err != nil && !kerr.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	Describe("Test", func() {

		Context("General", func() {

			Context("With PVC", func() {

				It("should run successfully", testGeneralBehaviour)
			})
		})

		Context("Streaming Replication", func() {

			var totalTable int

			var checkDataAcrossReplication = func() {
				if *postgres.Spec.StandbyMode == api.HotPostgresStandbyMode ||
					*postgres.Spec.StandbyMode == api.DeprecatedHotStandby {

					By("Checking Table in All pods [read-only connection behaviour]")
					f.CountFromAllPods(postgres.ObjectMeta, dbName, dbUser, totalTable)

				} else {
					// Default: warm standby
					By("Checking Table")
					f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
						Should(Equal(totalTable))

					By("Checking no read/write connection")
					f.EventuallyPingDatabase(
						postgres.ObjectMeta, f.GetArbitraryStandbyPodName(postgres.ObjectMeta), dbName, dbUser).
						Should(BeFalse())
				}
			}

			var createAndInsertData = func() {

				createAndWaitForRunning()

				By("Checking Streaming")
				f.EventuallyStreamingReplication(
					postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
					Should(Equal(int(*postgres.Spec.Replicas) - 1))

				By("Creating Schema")
				f.EventuallyCreateSchema(postgres.ObjectMeta, dbName, dbUser).
					Should(BeTrue())

				By("Creating Table")
				f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 3).
					Should(BeTrue())
				totalTable += 3

				checkDataAcrossReplication()
			}

			var createNewLeaderForcefully = func(manualNewLeader string) {
				// Manually change a new leader to the furthest pod {POD_NAME}-{N-1}
				// Also insert data in new master and check in other pods if the insert applied to them.

				// TODO: find more proper way to force new-leader selection

				By(fmt.Sprintf("Manually make %v the new Leader", manualNewLeader))
				f.MakeNewLeaderManually(postgres.ObjectMeta, manualNewLeader)

				By(fmt.Sprintf("Wait for %v to become leader node", manualNewLeader))
				f.EventuallyLeader(postgres.ObjectMeta).
					Should(Equal(manualNewLeader))

				By("Waiting for new primary database to be ready")
				f.EventuallyPingDatabase(
					postgres.ObjectMeta, manualNewLeader, dbName, dbUser).
					Should(BeTrue())

				By("Checking Streaming")
				f.EventuallyStreamingReplication(
					postgres.ObjectMeta, manualNewLeader, dbName, dbUser).
					Should(Equal(int(*postgres.Spec.Replicas) - 1))
			}

			var looseLeadershipByReducingReplica = func() {
				oldLeader := fmt.Sprintf("%v-%v", postgres.Name, *postgres.Spec.Replicas-1)

				// Now reduce replica by '1'. That way the latest primary node {POD_NAME}-{N-1} will be unavailable,
				// and a new master should be created as a part of failover.
				By("Reduce replicas by 1")
				pg, err := f.PatchPostgres(postgres.ObjectMeta, func(in *api.Postgres) *api.Postgres {
					replicas := *in.Spec.Replicas - 1
					in.Spec.Replicas = &replicas
					return in
				})
				Expect(err).NotTo(HaveOccurred())
				t1 := time.Now()
				postgres.Spec = pg.Spec

				By(fmt.Sprintf("Wait for %v to loose leadership", oldLeader))
				f.EventuallyLeader(postgres.ObjectMeta).
					ShouldNot(Equal(oldLeader))

				By("Wait for new master node")
				f.EventuallyLeaderExists(postgres.ObjectMeta).Should(BeTrue())
				diff := t1.Sub(time.Now())
				By(fmt.Sprintf("Took time to generate new master: %v", diff))

				By("Checking Streaming")
				f.EventuallyStreamingReplication(
					postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
					Should(Equal(int(*postgres.Spec.Replicas) - 1))
			}

			var shouldStreamSuccessfully = func() {
				createAndInsertData()

				// Delete and create again
				pauseAndResumeAgain()

				checkDataAcrossReplication()

				By("Checking Streaming")
				f.EventuallyStreamingReplication(
					postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
					Should(Equal(int(*postgres.Spec.Replicas) - 1))

				By("Creating Table")
				f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 2).
					Should(BeTrue())
				totalTable += 2

				checkDataAcrossReplication()
			}

			var shouldResumeAfterDeletion = func() {
				createAndInsertData()

				// Manually change a new leader to the furthest pod {POD_NAME}-{N-1}
				// Also insert data in new master and check in other pods if the insert applied to them.
				manualNewLeader := fmt.Sprintf("%v-%v", postgres.Name, *postgres.Spec.Replicas-1)
				createNewLeaderForcefully(manualNewLeader)

				By("Creating Table")
				f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 2).
					Should(BeTrue())
				totalTable += 2

				checkDataAcrossReplication()

				// Delete and create again
				pauseAndResumeAgain()

				checkDataAcrossReplication()

				By("Checking Streaming")
				f.EventuallyStreamingReplication(
					postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
					Should(Equal(int(*postgres.Spec.Replicas) - 1))

				By("Creating Table")
				f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 2).
					Should(BeTrue())
				totalTable += 2

				checkDataAcrossReplication()
			}

			var shouldFailoverSuccessfully = func() {
				// Objective: Make pod-{n-1} the primary node. Then reduce replica size.
				// Then, check if 'failover' is happening successfully.
				createAndInsertData()

				// Manually change a new leader to the furthest pod {POD_NAME}-{N-1}
				// Also insert data in new master and check in other pods if the insert applied to them.
				manualNewLeader := fmt.Sprintf("%v-%v", postgres.Name, *postgres.Spec.Replicas-1)
				createNewLeaderForcefully(manualNewLeader)

				By("Creating Table")
				f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 2).
					Should(BeTrue())
				totalTable += 2

				checkDataAcrossReplication()

				if postgres.Spec.Archiver != nil {
					By("Checking Archive")
					f.EventuallyCountArchive(
						postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
						Should(BeTrue())

					if !skipWalDataChecking {
						By("Checking wal data in backend")
						f.EventuallyWalDataFound(postgres).Should(BeTrue())
					}
				}

				// Now reduce replica by '1'. That way the latest primary node {POD_NAME}-{N-1} will be unavailable,
				// and a new master should be created as a part of failover.
				looseLeadershipByReducingReplica()

				By("Creating Table")
				f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 2).
					Should(BeTrue())
				totalTable += 2

				checkDataAcrossReplication()

				// Delete and create again
				pauseAndResumeAgain()

				checkDataAcrossReplication()

				By("Checking Streaming")
				f.EventuallyStreamingReplication(
					postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
					Should(Equal(int(*postgres.Spec.Replicas) - 1))

				By("Creating Table")
				f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 2).
					Should(BeTrue())
				totalTable += 2

				checkDataAcrossReplication()
			}

			var shouldFailoverConflictTimeline = func() {
				// Objective: Make pod-{n-1} the primary node. Then reduce replica size.
				// Then, check if 'failover' is happening successfully.
				createAndInsertData()

				manualNewLeader := fmt.Sprintf("%v-%v", postgres.Name, *postgres.Spec.Replicas-1)

				// Make a pod Master without affecting leadership.
				By(fmt.Sprintf("Manually make %v the new master", manualNewLeader))
				f.PromotePodToMaster(postgres.ObjectMeta, manualNewLeader)

				// Manually change a new leader to the furthest pod {POD_NAME}-{N-1}
				// Also insert data in new master and check in other pods if the insert applied to them.
				createNewLeaderForcefully(manualNewLeader)

				By("Creating Table")
				f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 2).
					Should(BeTrue())
				totalTable += 2

				checkDataAcrossReplication()

				if postgres.Spec.Archiver != nil {
					By("Checking Archive")
					f.EventuallyCountArchive(
						postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
						Should(BeTrue())

					if !skipWalDataChecking {
						By("Checking wal data in backend")
						f.EventuallyWalDataFound(postgres).Should(BeTrue())
					}
				}

				// Now reduce replica by '1'. That way the latest primary node {POD_NAME}-{N-1} will be unavailable,
				// and a new master should be created as a part of failover.
				looseLeadershipByReducingReplica()

				By("Creating Table")
				f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 2).
					Should(BeTrue())
				totalTable += 2

				checkDataAcrossReplication()

				// Delete and create again
				pauseAndResumeAgain()

				checkDataAcrossReplication()

				By("Checking Streaming")
				f.EventuallyStreamingReplication(
					postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
					Should(Equal(int(*postgres.Spec.Replicas) - 1))

				By("Creating Table")
				f.EventuallyCreateTable(
					postgres.ObjectMeta, dbName, dbUser, 2).
					Should(BeTrue())
				totalTable += 2

				checkDataAcrossReplication()
			}

			Context("Warm Standby", func() {
				BeforeEach(func() {
					standByMode := api.WarmPostgresStandbyMode
					postgres.Spec.StandbyMode = &standByMode
					totalTable = 0
					postgres.Spec.Replicas = types.Int32P(4)
					// Take liveness probe and reduce times
					demoPostgres := f.Postgres()
					demoPostgres.SetDefaults()
					postgres.Spec.PodTemplate.Spec.LivenessProbe = demoPostgres.Spec.PodTemplate.Spec.LivenessProbe
					postgres.Spec.PodTemplate.Spec.LivenessProbe.InitialDelaySeconds = 120
					postgres.Spec.PodTemplate.Spec.LivenessProbe.PeriodSeconds = 10
					// <== End
				})

				It("should stream successfully", shouldStreamSuccessfully)

				It("should resume when primary is not 0'th pod", shouldResumeAfterDeletion)

				It("should failover successfully", shouldFailoverSuccessfully)

				It("conflict timeline should failover successfully", shouldFailoverConflictTimeline)
			})

			Context("Hot Standby", func() {
				BeforeEach(func() {
					standByMode := api.HotPostgresStandbyMode
					postgres.Spec.StandbyMode = &standByMode
					totalTable = 0
					postgres.Spec.Replicas = types.Int32P(4)
					// Take liveness probe and reduce times
					demoPostgres := f.Postgres()
					demoPostgres.SetDefaults()
					postgres.Spec.PodTemplate.Spec.LivenessProbe = demoPostgres.Spec.PodTemplate.Spec.LivenessProbe
					postgres.Spec.PodTemplate.Spec.LivenessProbe.InitialDelaySeconds = 120
					postgres.Spec.PodTemplate.Spec.LivenessProbe.PeriodSeconds = 10
					// <== End
				})

				It("should stream successfully", shouldStreamSuccessfully)

				It("should resume when primary is not 0'th pod", shouldResumeAfterDeletion)

				It("should failover successfully", shouldFailoverSuccessfully)

				It("conflict timeline should failover successfully", shouldFailoverConflictTimeline)
			})

			Context("Archive with wal-g", func() {

				BeforeEach(func() {
					secret = f.SecretForS3Backend()
					totalTable = 0
					skipWalDataChecking = false
					postgres.Spec.Archiver = &api.PostgresArchiverSpec{
						Storage: &store.Backend{
							StorageSecretName: secret.Name,
							S3: &store.S3Spec{
								Bucket: os.Getenv(S3_BUCKET_NAME),
							},
						},
					}
				})

				var shouldArchiveAndInitialize = func() {
					oldPostgres, err := f.GetPostgres(postgres.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())

					garbagePostgres.Items = append(garbagePostgres.Items, *oldPostgres)

					// -- > 1st Postgres end < --

					// -- > 2nd Postgres < --
					*postgres = *f.Postgres()
					postgres.Spec.Replicas = types.Int32P(4)
					standByMode := api.HotPostgresStandbyMode
					postgres.Spec.StandbyMode = &standByMode
					postgres.Spec.DatabaseSecret = oldPostgres.Spec.DatabaseSecret
					postgres.Spec.Archiver = &api.PostgresArchiverSpec{
						Storage: &store.Backend{
							StorageSecretName: secret.Name,
							S3: &store.S3Spec{
								Bucket: os.Getenv(S3_BUCKET_NAME),
							},
						},
					}

					postgres.Spec.Init = &api.InitSpec{
						PostgresWAL: &api.PostgresWALSourceSpec{
							Backend: store.Backend{
								StorageSecretName: secret.Name,
								S3: &store.S3Spec{
									Bucket: os.Getenv(S3_BUCKET_NAME),
									Prefix: fmt.Sprintf("kubedb/%s/%s/archive/", postgres.Namespace, oldPostgres.Name),
								},
							},
						},
					}

					// Create Postgres
					createAndWaitForRunning()

					By("Ping Database")
					f.EventuallyPingDatabase(
						postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
						Should(BeTrue())

					By("Checking Table")
					f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
						Should(Equal(totalTable))

					By("Creating Table")
					f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 3).
						Should(BeTrue())
					totalTable += 3

					By("Checking Table")
					f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
						Should(Equal(totalTable))

					By("Checking Archive")
					f.EventuallyCountArchive(
						postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
						Should(BeTrue())

					if !skipWalDataChecking {
						By("Checking wal data in backend")
						f.EventuallyWalDataFound(postgres).Should(BeTrue())
					}

					oldPostgres, err = f.GetPostgres(postgres.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())

					garbagePostgres.Items = append(garbagePostgres.Items, *oldPostgres)

					// -- > 2nd Postgres end < --

					// -- > 3rd Postgres < --
					*postgres = *f.Postgres()
					postgres.Spec.Replicas = types.Int32P(4)
					standByMode = api.HotPostgresStandbyMode
					postgres.Spec.StandbyMode = &standByMode
					postgres.Spec.DatabaseSecret = oldPostgres.Spec.DatabaseSecret
					postgres.Spec.Init = &api.InitSpec{
						PostgresWAL: &api.PostgresWALSourceSpec{
							Backend: store.Backend{
								StorageSecretName: secret.Name,
								S3: &store.S3Spec{
									Bucket: os.Getenv(S3_BUCKET_NAME),
									Prefix: fmt.Sprintf("kubedb/%s/%s/archive/", postgres.Namespace, oldPostgres.Name),
								},
							},
						},
					}

					// Create Postgres
					createAndWaitForRunning()

					By("Ping Database")
					f.EventuallyPingDatabase(
						postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
						Should(BeTrue())

					By("Checking Table")
					f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
						Should(Equal(totalTable))
				}

				Context("Archive and Initialize from wal archive", func() {

					BeforeEach(func() {
						standByMode := api.HotPostgresStandbyMode
						postgres.Spec.StandbyMode = &standByMode
						totalTable = 0
						postgres.Spec.Replicas = types.Int32P(4)
					})

					It("should archive and should resume from archive successfully", func() {
						// -- > 1st Postgres < --
						err := f.CreateSecret(secret)
						Expect(err).NotTo(HaveOccurred())

						// Create Postgres
						createAndWaitForRunning()

						By("Creating Schema")
						f.EventuallyCreateSchema(postgres.ObjectMeta, dbName, dbUser).
							Should(BeTrue())

						By("Creating Table")
						f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 3).
							Should(BeTrue())
						totalTable += 3

						By("Checking Table")
						f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
							Should(Equal(totalTable))

						By("Checking Archive")
						f.EventuallyCountArchive(
							postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
							Should(BeTrue())

						if !skipWalDataChecking {
							By("Checking wal data in backend")
							f.EventuallyWalDataFound(postgres).Should(BeTrue())
						}

						// Delete and create again
						pauseAndResumeAgain()

						checkDataAcrossReplication()

						By("Checking Streaming")
						f.EventuallyStreamingReplication(
							postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
							Should(Equal(int(*postgres.Spec.Replicas) - 1))

						shouldArchiveAndInitialize()
					})
				})

				Context("failover scenerio", func() {

					Context("Hot Standby", func() {

						BeforeEach(func() {
							standByMode := api.HotPostgresStandbyMode
							postgres.Spec.StandbyMode = &standByMode
							totalTable = 0
							postgres.Spec.Replicas = types.Int32P(4)
							// Take liveness probe and reduce times
							demoPostgres := f.Postgres()
							demoPostgres.SetDefaults()
							postgres.Spec.PodTemplate.Spec.LivenessProbe = demoPostgres.Spec.PodTemplate.Spec.LivenessProbe
							postgres.Spec.PodTemplate.Spec.LivenessProbe.InitialDelaySeconds = 300
							postgres.Spec.PodTemplate.Spec.LivenessProbe.PeriodSeconds = 10
							// <== End

						})

						It("should archive and initialize when 0'th pod is not leader", func() {
							// -- > 1st Postgres < --
							err := f.CreateSecret(secret)
							Expect(err).NotTo(HaveOccurred())

							// Create Postgres
							shouldResumeAfterDeletion()

							// Now test initialize fromarchive
							shouldArchiveAndInitialize()
						})

						It("should archive and initialize in failover scenario", func() {
							// -- > 1st Postgres < --
							err := f.CreateSecret(secret)
							Expect(err).NotTo(HaveOccurred())

							// Create Postgres
							shouldFailoverSuccessfully()

							// Now test initialize fromarchive
							shouldArchiveAndInitialize()
						})

						It("should archive and initialize in conflict timeline failover scenario", func() {
							// -- > 1st Postgres < --
							err := f.CreateSecret(secret)
							Expect(err).NotTo(HaveOccurred())

							// Create Postgres
							shouldFailoverConflictTimeline()

							// Now test initialize fromarchive
							shouldArchiveAndInitialize()
						})
					})

					FContext("Warm Standby", func() {

						BeforeEach(func() {
							standByMode := api.WarmPostgresStandbyMode
							postgres.Spec.StandbyMode = &standByMode
							totalTable = 0
							postgres.Spec.Replicas = types.Int32P(4)
							// Take liveness probe and reduce times
							demoPostgres := f.Postgres()
							demoPostgres.SetDefaults()
							postgres.Spec.PodTemplate.Spec.LivenessProbe = demoPostgres.Spec.PodTemplate.Spec.LivenessProbe
							postgres.Spec.PodTemplate.Spec.LivenessProbe.InitialDelaySeconds = 300
							postgres.Spec.PodTemplate.Spec.LivenessProbe.PeriodSeconds = 10
							// <== End

						})

						It("should archive and initialize when 0'th pod is not leader", func() {
							// -- > 1st Postgres < --
							err := f.CreateSecret(secret)
							Expect(err).NotTo(HaveOccurred())

							// Create Postgres
							shouldResumeAfterDeletion()

							// Now test initialize fromarchive
							shouldArchiveAndInitialize()
						})

						It("should archive and initialize in failover scenario", func() {
							// -- > 1st Postgres < --
							err := f.CreateSecret(secret)
							Expect(err).NotTo(HaveOccurred())

							// Create Postgres
							shouldFailoverSuccessfully()

							// Now test initialize fromarchive
							shouldArchiveAndInitialize()
						})

						It("should archive and initialize in conflict timeline failover scenario", func() {
							// -- > 1st Postgres < --
							err := f.CreateSecret(secret)
							Expect(err).NotTo(HaveOccurred())

							// Create Postgres
							shouldFailoverConflictTimeline()

							// Now test initialize fromarchive
							shouldArchiveAndInitialize()
						})
					})
				})

				Context("WipeOut wal data", func() {

					BeforeEach(func() {
						postgres.Spec.TerminationPolicy = api.TerminationPolicyWipeOut
					})

					It("should remove wal data from backend", func() {

						err := f.CreateSecret(secret)
						Expect(err).NotTo(HaveOccurred())

						// Create Postgres
						createAndWaitForRunning()

						By("Creating Schema")
						f.EventuallyCreateSchema(postgres.ObjectMeta, dbName, dbUser).
							Should(BeTrue())

						By("Creating Table")
						f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 3).
							Should(BeTrue())

						By("Checking Table")
						f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
							Should(Equal(3))

						By("Checking Archive")
						f.EventuallyCountArchive(
							postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
							Should(BeTrue())

						By("Checking wal data in backend")
						f.EventuallyWalDataFound(postgres).Should(BeTrue())

						By("Deleting Postgres crd")
						err = f.DeletePostgres(postgres.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())

						By("Checking DormantDatabase is not created")
						f.EventuallyDormantDatabase(postgres.ObjectMeta).Should(BeFalse())

						By("Checking Wal data removed from backend")
						f.EventuallyWalDataFound(postgres).Should(BeFalse())
					})
				})
			})
		})

		Context("Snapshot", func() {

			BeforeEach(func() {
				skipSnapshotDataChecking = false
				snapshot.Spec.DatabaseName = postgres.Name
			})

			Context("In Local", func() {

				BeforeEach(func() {
					skipSnapshotDataChecking = true
					secret = f.SecretForLocalBackend()
					snapshot.Spec.StorageSecretName = secret.Name
				})

				Context("With EmptyDir as Snapshot's backend", func() {
					BeforeEach(func() {
						snapshot.Spec.Local = &store.LocalSpec{
							MountPath: "/repo",
							VolumeSource: core.VolumeSource{
								EmptyDir: &core.EmptyDirVolumeSource{},
							},
						}
					})

					It("should take Snapshot successfully", shouldTakeSnapshot)
				})

				Context("With PVC as Snapshot's backend", func() {
					var snapPVC *core.PersistentVolumeClaim

					BeforeEach(func() {
						snapPVC = f.GetPersistentVolumeClaim()
						err := f.CreatePersistentVolumeClaim(snapPVC)
						Expect(err).NotTo(HaveOccurred())

						snapshot.Spec.Local = &store.LocalSpec{
							MountPath: "/repo",
							VolumeSource: core.VolumeSource{
								PersistentVolumeClaim: &core.PersistentVolumeClaimVolumeSource{
									ClaimName: snapPVC.Name,
								},
							},
						}
					})

					AfterEach(func() {
						err := f.DeletePersistentVolumeClaim(snapPVC.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())
					})

					It("should delete Snapshot successfully", func() {
						shouldTakeSnapshot()

						By("Deleting Snapshot")
						err := f.DeleteSnapshot(snapshot.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())

						By("Waiting Snapshot to be deleted")
						f.EventuallySnapshot(snapshot.ObjectMeta).Should(BeFalse())
					})
				})
			})

			Context("In S3", func() {

				BeforeEach(func() {
					secret = f.SecretForS3Backend()
					snapshot.Spec.StorageSecretName = secret.Name
					snapshot.Spec.S3 = &store.S3Spec{
						Bucket: os.Getenv(S3_BUCKET_NAME),
					}
				})

				It("should take Snapshot successfully", shouldTakeSnapshot)

				Context("Delete One Snapshot keeping others", func() {

					BeforeEach(func() {
						postgres.Spec.Init = &api.InitSpec{
							ScriptSource: &api.ScriptSourceSpec{
								VolumeSource: core.VolumeSource{
									GitRepo: &core.GitRepoVolumeSource{
										Repository: "https://github.com/kubedb/postgres-init-scripts.git",
										Directory:  ".",
									},
								},
							},
						}
					})

					It("Delete One Snapshot keeping others", func() {
						// Create Postgres and take Snapshot
						shouldTakeSnapshot()

						oldSnapshot := snapshot.DeepCopy()

						// New snapshot that has old snapshot's name in prefix
						snapshot.Name += "-2"

						By(fmt.Sprintf("Create Snapshot %v", snapshot.Name))
						err = f.CreateSnapshot(snapshot)
						Expect(err).NotTo(HaveOccurred())

						By("Check for Succeeded snapshot")
						f.EventuallySnapshotPhase(snapshot.ObjectMeta).Should(Equal(api.SnapshotPhaseSucceeded))

						if !skipSnapshotDataChecking {
							By("Check for snapshot data")
							f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
						}

						// delete old snapshot
						By(fmt.Sprintf("Delete old Snapshot %v", snapshot.Name))
						err = f.DeleteSnapshot(oldSnapshot.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())

						By("Waiting for old Snapshot to be deleted")
						f.EventuallySnapshot(oldSnapshot.ObjectMeta).Should(BeFalse())
						if !skipSnapshotDataChecking {
							By(fmt.Sprintf("Check data for old snapshot %v", oldSnapshot.Name))
							f.EventuallySnapshotDataFound(oldSnapshot).Should(BeFalse())
						}

						// check remaining snapshot
						By(fmt.Sprintf("Checking another Snapshot %v still exists", snapshot.Name))
						_, err = f.GetSnapshot(snapshot.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())

						if !skipSnapshotDataChecking {
							By(fmt.Sprintf("Check data for remaining snapshot %v", snapshot.Name))
							f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
						}
					})
				})
			})

			Context("In GCS", func() {

				BeforeEach(func() {
					secret = f.SecretForGCSBackend()
					snapshot.Spec.StorageSecretName = secret.Name
					snapshot.Spec.GCS = &store.GCSSpec{
						Bucket: os.Getenv(GCS_BUCKET_NAME),
					}
				})

				It("should take Snapshot successfully", shouldTakeSnapshot)

				Context("faulty snapshot", func() {
					BeforeEach(func() {
						skipSnapshotDataChecking = true
						snapshot.Spec.StorageSecretName = secret.Name
						snapshot.Spec.GCS = &store.GCSSpec{
							Bucket: "nonexisting",
						}
					})
					It("snapshot should fail", func() {
						// Create and wait for running db
						createAndWaitForRunning()

						By("Create Secret")
						err := f.CreateSecret(secret)
						Expect(err).NotTo(HaveOccurred())

						By("Create Snapshot")
						err = f.CreateSnapshot(snapshot)
						Expect(err).NotTo(HaveOccurred())

						By("Check for failed snapshot")
						f.EventuallySnapshotPhase(snapshot.ObjectMeta).Should(Equal(api.SnapshotPhaseFailed))
					})
				})

				Context("Delete One Snapshot keeping others", func() {

					BeforeEach(func() {
						postgres.Spec.Init = &api.InitSpec{
							ScriptSource: &api.ScriptSourceSpec{
								VolumeSource: core.VolumeSource{
									GitRepo: &core.GitRepoVolumeSource{
										Repository: "https://github.com/kubedb/postgres-init-scripts.git",
										Directory:  ".",
									},
								},
							},
						}
					})

					It("Delete One Snapshot keeping others", func() {
						// Create Postgres and take Snapshot
						shouldTakeSnapshot()

						oldSnapshot := snapshot.DeepCopy()

						// New snapshot that has old snapshot's name in prefix
						snapshot.Name += "-2"

						By(fmt.Sprintf("Create Snapshot %v", snapshot.Name))
						err = f.CreateSnapshot(snapshot)
						Expect(err).NotTo(HaveOccurred())

						By("Check for Succeeded snapshot")
						f.EventuallySnapshotPhase(snapshot.ObjectMeta).Should(Equal(api.SnapshotPhaseSucceeded))

						if !skipSnapshotDataChecking {
							By("Check for snapshot data")
							f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
						}

						// delete old snapshot
						By(fmt.Sprintf("Delete old Snapshot %v", snapshot.Name))
						err = f.DeleteSnapshot(oldSnapshot.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())

						By("Waiting for old Snapshot to be deleted")
						f.EventuallySnapshot(oldSnapshot.ObjectMeta).Should(BeFalse())
						if !skipSnapshotDataChecking {
							By(fmt.Sprintf("Check data for old snapshot %v", oldSnapshot.Name))
							f.EventuallySnapshotDataFound(oldSnapshot).Should(BeFalse())
						}

						// check remaining snapshot
						By(fmt.Sprintf("Checking another Snapshot %v still exists", snapshot.Name))
						_, err = f.GetSnapshot(snapshot.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())

						if !skipSnapshotDataChecking {
							By(fmt.Sprintf("Check data for remaining snapshot %v", snapshot.Name))
							f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
						}
					})
				})
			})

			Context("In Azure", func() {

				BeforeEach(func() {
					secret = f.SecretForAzureBackend()
					snapshot.Spec.StorageSecretName = secret.Name
					snapshot.Spec.Azure = &store.AzureSpec{
						Container: os.Getenv(AZURE_CONTAINER_NAME),
					}
				})

				It("should take Snapshot successfully", shouldTakeSnapshot)

				Context("Delete One Snapshot keeping others", func() {

					BeforeEach(func() {
						postgres.Spec.Init = &api.InitSpec{
							ScriptSource: &api.ScriptSourceSpec{
								VolumeSource: core.VolumeSource{
									GitRepo: &core.GitRepoVolumeSource{
										Repository: "https://github.com/kubedb/postgres-init-scripts.git",
										Directory:  ".",
									},
								},
							},
						}
					})

					It("Delete One Snapshot keeping others", func() {
						// Create Postgres and take Snapshot
						shouldTakeSnapshot()

						oldSnapshot := snapshot.DeepCopy()

						// New snapshot that has old snapshot's name in prefix
						snapshot.Name += "-2"

						By(fmt.Sprintf("Create Snapshot %v", snapshot.Name))
						err = f.CreateSnapshot(snapshot)
						Expect(err).NotTo(HaveOccurred())

						By("Check for Succeeded snapshot")
						f.EventuallySnapshotPhase(snapshot.ObjectMeta).Should(Equal(api.SnapshotPhaseSucceeded))

						if !skipSnapshotDataChecking {
							By("Check for snapshot data")
							f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
						}

						// delete old snapshot
						By(fmt.Sprintf("Delete old Snapshot %v", snapshot.Name))
						err = f.DeleteSnapshot(oldSnapshot.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())

						By("Waiting for old Snapshot to be deleted")
						f.EventuallySnapshot(oldSnapshot.ObjectMeta).Should(BeFalse())
						if !skipSnapshotDataChecking {
							By(fmt.Sprintf("Check data for old snapshot %v", oldSnapshot.Name))
							f.EventuallySnapshotDataFound(oldSnapshot).Should(BeFalse())
						}

						// check remaining snapshot
						By(fmt.Sprintf("Checking another Snapshot %v still exists", snapshot.Name))
						_, err = f.GetSnapshot(snapshot.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())

						if !skipSnapshotDataChecking {
							By(fmt.Sprintf("Check data for remaining snapshot %v", snapshot.Name))
							f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
						}
					})
				})
			})

			Context("In Swift", func() {

				BeforeEach(func() {
					secret = f.SecretForSwiftBackend()
					snapshot.Spec.StorageSecretName = secret.Name
					snapshot.Spec.Swift = &store.SwiftSpec{
						Container: os.Getenv(SWIFT_CONTAINER_NAME),
					}
				})

				It("should take Snapshot successfully", shouldTakeSnapshot)
			})
		})

		Context("Initialize", func() {

			Context("With Script", func() {

				BeforeEach(func() {
					postgres.Spec.Init = &api.InitSpec{
						ScriptSource: &api.ScriptSourceSpec{
							VolumeSource: core.VolumeSource{
								GitRepo: &core.GitRepoVolumeSource{
									Repository: "https://github.com/kubedb/postgres-init-scripts.git",
									Directory:  ".",
								},
							},
						},
					}
				})

				It("should run successfully", func() {
					// Create Postgres
					createAndWaitForRunning()

					By("Checking Table")
					f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
						Should(Equal(1))
				})

			})

			Context("With Snapshot", func() {

				var shouldInitializeFromSnapshot = func() {
					// create postgres and take snapshot
					shouldInsertDataAndTakeSnapshot()

					oldPostgres, err := f.GetPostgres(postgres.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())

					garbagePostgres.Items = append(garbagePostgres.Items, *oldPostgres)

					By("Create postgres from snapshot")
					*postgres = *f.Postgres()
					postgres.Spec.DatabaseSecret = oldPostgres.Spec.DatabaseSecret
					postgres.Spec.Init = &api.InitSpec{
						SnapshotSource: &api.SnapshotSourceSpec{
							Namespace: snapshot.Namespace,
							Name:      snapshot.Name,
						},
					}

					// Create and wait for running Postgres
					createAndWaitForRunning()

					By("Checking Table")
					f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
						Should(Equal(3))
				}

				Context("From Local backend", func() {
					var snapPVC *core.PersistentVolumeClaim

					BeforeEach(func() {

						skipSnapshotDataChecking = true
						snapPVC = f.GetPersistentVolumeClaim()
						err := f.CreatePersistentVolumeClaim(snapPVC)
						Expect(err).NotTo(HaveOccurred())

						secret = f.SecretForLocalBackend()
						snapshot.Spec.DatabaseName = postgres.Name
						snapshot.Spec.StorageSecretName = secret.Name

						snapshot.Spec.Local = &store.LocalSpec{
							MountPath: "/repo",
							VolumeSource: core.VolumeSource{
								PersistentVolumeClaim: &core.PersistentVolumeClaimVolumeSource{
									ClaimName: snapPVC.Name,
								},
							},
						}
					})

					AfterEach(func() {
						err := f.DeletePersistentVolumeClaim(snapPVC.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())
					})

					It("should initialize successfully", shouldInitializeFromSnapshot)
				})

				Context("From GCS backend", func() {

					BeforeEach(func() {

						skipSnapshotDataChecking = false
						secret = f.SecretForGCSBackend()
						snapshot.Spec.StorageSecretName = secret.Name
						snapshot.Spec.DatabaseName = postgres.Name

						snapshot.Spec.GCS = &store.GCSSpec{
							Bucket: os.Getenv(GCS_BUCKET_NAME),
						}
					})

					It("should run successfully", shouldInitializeFromSnapshot)
				})
			})
		})

		Context("Resume", func() {
			var usedInitialized bool

			BeforeEach(func() {
				usedInitialized = false
			})

			var shouldResumeSuccessfully = func() {
				// Create and wait for running Postgres
				createAndWaitForRunning()

				pauseAndResumeAgain()

				pg, err := f.GetPostgres(postgres.ObjectMeta)
				Expect(err).NotTo(HaveOccurred())

				*postgres = *pg
				if usedInitialized {
					_, ok := postgres.Annotations[api.AnnotationInitialized]
					Expect(ok).Should(BeTrue())
				}
			}

			Context("-", func() {
				It("should resume DormantDatabase successfully", shouldResumeSuccessfully)
			})

			Context("With Init", func() {

				BeforeEach(func() {
					postgres.Spec.Init = &api.InitSpec{
						ScriptSource: &api.ScriptSourceSpec{
							VolumeSource: core.VolumeSource{
								GitRepo: &core.GitRepoVolumeSource{
									Repository: "https://github.com/kubedb/postgres-init-scripts.git",
									Directory:  ".",
								},
							},
						},
					}
				})

				It("should resume DormantDatabase successfully", shouldResumeSuccessfully)
			})

			Context("With Snapshot Init", func() {

				BeforeEach(func() {
					skipSnapshotDataChecking = false
					secret = f.SecretForGCSBackend()
					snapshot.Spec.StorageSecretName = secret.Name
					snapshot.Spec.GCS = &store.GCSSpec{
						Bucket: os.Getenv(GCS_BUCKET_NAME),
					}
					snapshot.Spec.DatabaseName = postgres.Name
				})

				It("should resume successfully", func() {
					// create postgres and take snapshot
					shouldInsertDataAndTakeSnapshot()

					oldPostgres, err := f.GetPostgres(postgres.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())

					garbagePostgres.Items = append(garbagePostgres.Items, *oldPostgres)

					By("Create postgres from snapshot")
					*postgres = *f.Postgres()
					postgres.Spec.Init = &api.InitSpec{
						SnapshotSource: &api.SnapshotSourceSpec{
							Namespace: snapshot.Namespace,
							Name:      snapshot.Name,
						},
					}

					By("Creating init Snapshot Postgres without secret name" + postgres.Name)
					err = f.CreatePostgres(postgres)
					Expect(err).Should(HaveOccurred())

					// for snapshot init, user have to use older secret,
					postgres.Spec.DatabaseSecret = oldPostgres.Spec.DatabaseSecret
					// Create and wait for running Postgres
					createAndWaitForRunning()

					By("Ping Database")
					f.EventuallyPingDatabase(
						postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
						Should(BeTrue())

					By("Checking Table")
					f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
						Should(Equal(3))

					By("Again delete and resume  " + postgres.Name)

					pauseAndResumeAgain()

					postgres, err = f.GetPostgres(postgres.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())
					Expect(postgres.Spec.Init).ShouldNot(BeNil())

					By("Ping Database")
					f.EventuallyPingDatabase(
						postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser).
						Should(BeTrue())

					By("Checking Table")
					f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
						Should(Equal(3))

					By("Checking postgres crd has kubedb.com/initialized annotation")
					_, err = meta_util.GetString(postgres.Annotations, api.AnnotationInitialized)
					Expect(err).NotTo(HaveOccurred())
				})
			})

			Context("Resume Multiple times - with init", func() {

				BeforeEach(func() {
					usedInitialized = true
					postgres.Spec.Init = &api.InitSpec{
						ScriptSource: &api.ScriptSourceSpec{
							ScriptPath: "postgres-init-scripts/run.sh",
							VolumeSource: core.VolumeSource{
								GitRepo: &core.GitRepoVolumeSource{
									Repository: "https://github.com/kubedb/postgres-init-scripts.git",
								},
							},
						},
					}
				})

				It("should resume DormantDatabase successfully", func() {
					// Create and wait for running Postgres
					createAndWaitForRunning()

					for i := 0; i < 3; i++ {
						By(fmt.Sprintf("%v-th", i+1) + " time running.")

						pauseAndResumeAgain()

						_, err = f.GetPostgres(postgres.ObjectMeta)
						Expect(err).NotTo(HaveOccurred())
					}
				})
			})
		})

		Context("SnapshotScheduler", func() {

			BeforeEach(func() {
				secret = f.SecretForLocalBackend()
			})

			Context("With Startup", func() {

				BeforeEach(func() {
					postgres.Spec.BackupSchedule = &api.BackupScheduleSpec{
						CronExpression: "@every 1m",
						Backend: store.Backend{
							StorageSecretName: secret.Name,
							Local: &store.LocalSpec{
								MountPath: "/repo",
								VolumeSource: core.VolumeSource{
									EmptyDir: &core.EmptyDirVolumeSource{},
								},
							},
						},
					}
				})

				It("should run scheduler successfully", func() {
					By("Create Secret")
					err := f.CreateSecret(secret)
					Expect(err).NotTo(HaveOccurred())

					// Create and wait for running Postgres
					createAndWaitForRunning()

					By("Count multiple Snapshot")
					f.EventuallySnapshotCount(postgres.ObjectMeta).Should(matcher.MoreThan(3))
				})
			})

			Context("With Update", func() {
				It("should run scheduler successfully", func() {
					// Create and wait for running Postgres
					createAndWaitForRunning()

					By("Create Secret")
					err := f.CreateSecret(secret)
					Expect(err).NotTo(HaveOccurred())

					By("Update postgres")
					_, err = f.PatchPostgres(postgres.ObjectMeta, func(in *api.Postgres) *api.Postgres {
						in.Spec.BackupSchedule = &api.BackupScheduleSpec{
							CronExpression: "@every 1m",
							Backend: store.Backend{
								StorageSecretName: secret.Name,
								Local: &store.LocalSpec{
									MountPath: "/repo",
									VolumeSource: core.VolumeSource{
										EmptyDir: &core.EmptyDirVolumeSource{},
									},
								},
							},
						}

						return in
					})
					Expect(err).NotTo(HaveOccurred())

					By("Count multiple Snapshot")
					f.EventuallySnapshotCount(postgres.ObjectMeta).Should(matcher.MoreThan(3))
				})
			})
		})

		Context("Termination Policy", func() {

			BeforeEach(func() {
				skipSnapshotDataChecking = false
				secret = f.SecretForGCSBackend()
				snapshot.Spec.StorageSecretName = secret.Name
				snapshot.Spec.GCS = &store.GCSSpec{
					Bucket: os.Getenv(GCS_BUCKET_NAME),
				}
				snapshot.Spec.DatabaseName = postgres.Name
			})

			Context("with TerminationPolicyDoNotTerminate", func() {

				BeforeEach(func() {
					skipSnapshotDataChecking = true
					postgres.Spec.TerminationPolicy = api.TerminationPolicyDoNotTerminate
				})

				It("should work successfully", func() {
					// Create and wait for running Postgres
					createAndWaitForRunning()

					By("Delete postgres")
					err = f.DeletePostgres(postgres.ObjectMeta)
					Expect(err).Should(HaveOccurred())

					By("Postgres is not paused. Check for postgres")
					f.EventuallyPostgres(postgres.ObjectMeta).Should(BeTrue())

					By("Check for Running postgres")
					f.EventuallyPostgresRunning(postgres.ObjectMeta).Should(BeTrue())

					By("Update postgres to set spec.terminationPolicy = Pause")
					_, err := f.PatchPostgres(postgres.ObjectMeta, func(in *api.Postgres) *api.Postgres {
						in.Spec.TerminationPolicy = api.TerminationPolicyPause
						return in
					})
					Expect(err).NotTo(HaveOccurred())
				})
			})

			Context("with TerminationPolicyPause (default)", func() {

				It("should create DormantDatabase and resume from it", func() {
					// Run Postgres and take snapshot
					shouldInsertDataAndTakeSnapshot()

					By("Deleting Postgres crd")
					err = f.DeletePostgres(postgres.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())

					// DormantDatabase.Status= paused, means postgres object is deleted
					By("Waiting for postgres to be paused")
					f.EventuallyDormantDatabaseStatus(postgres.ObjectMeta).
						Should(matcher.HavePaused())

					By("Checking PVC hasn't been deleted")
					f.EventuallyPVCCount(postgres.ObjectMeta).Should(Equal(1))

					By("Checking Secret hasn't been deleted")
					f.EventuallyDBSecretCount(postgres.ObjectMeta).Should(Equal(1))

					By("Checking snapshot hasn't been deleted")
					f.EventuallySnapshot(snapshot.ObjectMeta).Should(BeTrue())

					if !skipSnapshotDataChecking {
						By("Check for snapshot data")
						f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
					}

					// Create Postgres object again to resume it
					By("Create (resume) Postgres: " + postgres.Name)
					err = f.CreatePostgres(postgres)
					Expect(err).NotTo(HaveOccurred())

					By("Wait for DormantDatabase to be deleted")
					f.EventuallyDormantDatabase(postgres.ObjectMeta).Should(BeFalse())

					By("Wait for Running postgres")
					f.EventuallyPostgresRunning(postgres.ObjectMeta).Should(BeTrue())

					By("Checking Table")
					f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
						Should(Equal(3))
				})
			})

			Context("with TerminationPolicyDelete", func() {

				BeforeEach(func() {
					postgres.Spec.TerminationPolicy = api.TerminationPolicyDelete
				})

				AfterEach(func() {
					By("Deleting snapshot: " + snapshot.Name)
					err := f.DeleteSnapshot(snapshot.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())
				})

				It("should not create DormantDatabase and should not delete secret and snapshot", func() {
					// Run Postgres and take snapshot
					shouldInsertDataAndTakeSnapshot()

					By("Delete postgres")
					err = f.DeletePostgres(postgres.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())

					By("wait until postgres is deleted")
					f.EventuallyPostgres(postgres.ObjectMeta).Should(BeFalse())

					By("Checking DormantDatabase is not created")
					f.EventuallyDormantDatabase(postgres.ObjectMeta).Should(BeFalse())

					By("Checking PVC has been deleted")
					f.EventuallyPVCCount(postgres.ObjectMeta).Should(Equal(0))

					By("Checking Secret hasn't been deleted")
					f.EventuallyDBSecretCount(postgres.ObjectMeta).Should(Equal(1))

					By("Checking Snapshot hasn't been deleted")
					f.EventuallySnapshot(snapshot.ObjectMeta).Should(BeTrue())

					if !skipSnapshotDataChecking {
						By("Check for intact snapshot data")
						f.EventuallySnapshotDataFound(snapshot).Should(BeTrue())
					}
				})
			})

			Context("with TerminationPolicyWipeOut", func() {

				BeforeEach(func() {
					postgres.Spec.TerminationPolicy = api.TerminationPolicyWipeOut
				})

				It("should not create DormantDatabase and should wipeOut all", func() {
					// Run Postgres and take snapshot
					shouldInsertDataAndTakeSnapshot()

					By("Delete postgres")
					err = f.DeletePostgres(postgres.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())

					By("wait until postgres is deleted")
					f.EventuallyPostgres(postgres.ObjectMeta).Should(BeFalse())

					By("Checking DormantDatabase is not created")
					f.EventuallyDormantDatabase(postgres.ObjectMeta).Should(BeFalse())

					By("Checking PVCs has been deleted")
					f.EventuallyPVCCount(postgres.ObjectMeta).Should(Equal(0))

					By("Checking Snapshots has been deleted")
					f.EventuallySnapshot(snapshot.ObjectMeta).Should(BeFalse())

					By("Checking Secrets has been deleted")
					f.EventuallyDBSecretCount(postgres.ObjectMeta).Should(Equal(0))
				})
			})
		})

		Context("EnvVars", func() {

			Context("With all supported EnvVars", func() {

				It("should create DB with provided EnvVars", func() {
					if skipMessage != "" {
						Skip(skipMessage)
					}

					const (
						dataDir = "/var/pv/pgdata"
						walDir  = "/var/pv/wal"
					)
					dbName = f.App()
					postgres.Spec.PodTemplate.Spec.Env = []core.EnvVar{
						{
							Name:  PGDATA,
							Value: dataDir,
						},
						{
							Name:  POSTGRES_DB,
							Value: dbName,
						},
						{
							Name:  POSTGRES_INITDB_ARGS,
							Value: "--data-checksums",
						},
					}

					walEnv := []core.EnvVar{
						{
							Name:  POSTGRES_INITDB_WALDIR,
							Value: walDir,
						},
					}

					if strings.HasPrefix(framework.DBVersion, "9") {
						walEnv = []core.EnvVar{
							{
								Name:  POSTGRES_INITDB_XLOGDIR,
								Value: walDir,
							},
						}
					}
					postgres.Spec.PodTemplate.Spec.Env = core_util.UpsertEnvVars(
						postgres.Spec.PodTemplate.Spec.Env, walEnv...,
					)

					// Run Postgres with provided Environment Variables
					testGeneralBehaviour()
				})
			})

			Context("Root Password as EnvVar", func() {

				It("should reject to create Postgres CRD", func() {
					if skipMessage != "" {
						Skip(skipMessage)
					}

					dbName = f.App()
					postgres.Spec.PodTemplate.Spec.Env = []core.EnvVar{
						{
							Name:  POSTGRES_PASSWORD,
							Value: "not@secret",
						},
					}

					By("Creating Posgres: " + postgres.Name)
					err = f.CreatePostgres(postgres)
					Expect(err).To(HaveOccurred())
				})
			})

			Context("Update EnvVar", func() {

				It("should not reject to update EnvVar", func() {
					if skipMessage != "" {
						Skip(skipMessage)
					}

					dbName = f.App()
					postgres.Spec.PodTemplate.Spec.Env = []core.EnvVar{
						{
							Name:  POSTGRES_DB,
							Value: dbName,
						},
					}

					// Run Postgres with provided Environment Variables
					testGeneralBehaviour()

					By("Patching EnvVar")
					_, _, err = util.PatchPostgres(f.ExtClient().KubedbV1alpha1(), postgres,
						func(in *api.Postgres) *api.Postgres {
							in.Spec.PodTemplate.Spec.Env = []core.EnvVar{
								{
									Name:  POSTGRES_DB,
									Value: "patched-db",
								},
							}
							return in
						})
					Expect(err).NotTo(HaveOccurred())
				})
			})
		})

		Context("Custom config", func() {

			customConfigs := []string{
				"shared_buffers=256MB",
				"max_connections=300",
			}

			Context("from configMap", func() {
				var userConfig *core.ConfigMap

				BeforeEach(func() {
					userConfig = f.GetCustomConfig(customConfigs)
				})

				AfterEach(func() {
					By("Deleting configMap: " + userConfig.Name)
					err := f.DeleteConfigMap(userConfig.ObjectMeta)
					Expect(err).NotTo(HaveOccurred())
				})

				It("should set configuration provided in configMap", func() {
					if skipMessage != "" {
						Skip(skipMessage)
					}

					By("Creating configMap: " + userConfig.Name)
					err := f.CreateConfigMap(userConfig)
					Expect(err).NotTo(HaveOccurred())

					postgres.Spec.ConfigSource = &core.VolumeSource{
						ConfigMap: &core.ConfigMapVolumeSource{
							LocalObjectReference: core.LocalObjectReference{
								Name: userConfig.Name,
							},
						},
					}

					// Create Postgres
					createAndWaitForRunning()

					By("Checking postgres configured from provided custom configuration")
					for _, cfg := range customConfigs {
						f.EventuallyPGSettings(
							postgres.ObjectMeta, f.GetPrimaryPodName(postgres.ObjectMeta), dbName, dbUser, cfg).
							Should(matcher.Use(cfg))
					}
				})
			})
		})

		Context("StorageType ", func() {

			var shouldRunSuccessfully = func() {
				if skipMessage != "" {
					Skip(skipMessage)
				}
				// Create Postgres
				createAndWaitForRunning()

				By("Creating Schema")
				f.EventuallyCreateSchema(postgres.ObjectMeta, dbName, dbUser).
					Should(BeTrue())

				By("Creating Table")
				f.EventuallyCreateTable(postgres.ObjectMeta, dbName, dbUser, 3).
					Should(BeTrue())

				By("Checking Table")
				f.EventuallyCountTableFromPrimary(postgres.ObjectMeta, dbName, dbUser).
					Should(Equal(3))
			}

			Context("Ephemeral", func() {

				BeforeEach(func() {
					postgres.Spec.StorageType = api.StorageTypeEphemeral
					postgres.Spec.Storage = nil
				})

				Context("General Behaviour", func() {

					BeforeEach(func() {
						postgres.Spec.TerminationPolicy = api.TerminationPolicyWipeOut
					})

					It("should run successfully", shouldRunSuccessfully)
				})

				Context("With TerminationPolicyPause", func() {

					BeforeEach(func() {
						postgres.Spec.TerminationPolicy = api.TerminationPolicyPause
					})

					It("should reject to create Postgres object", func() {
						By("Creating Postgres: " + postgres.Name)
						err := f.CreatePostgres(postgres)
						Expect(err).To(HaveOccurred())
					})
				})
			})
		})
	})
})

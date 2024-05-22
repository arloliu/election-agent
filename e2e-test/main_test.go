package e2etest

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/support/kind"
	"sigs.k8s.io/e2e-framework/support/utils"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type dockerImage struct {
	file  string
	image string
}

var (
	testEnv      env.Environment
	kindImage    = "kindest/node:v1.24.17"
	clusterName  = "election-agnet-e2e-test"
	namespace    = "e2e-test"
	dockerImages = []dockerImage{
		{file: "build/Dockerfile.ea", image: "election-agent:e2e-test"},
		{file: "build/Dockerfile.zc", image: "zone-coordinator:e2e-test"},
		{file: "build/Dockerfile.util", image: "election-agent-util:e2e-test"},
	}
	// parameters
	activeZoneTimeout  = 20 * time.Second
	stateChangeTimeout = 60 * time.Second
	simDuration        = 15 * time.Second
	simNumClients      = 1000
	featureIterations  = 1
)

func TestMain(m *testing.M) { //nolint:cyclop,gocyclo
	cfg, err := envconf.NewFromFlags()
	if err != nil {
		log.Fatalf("failed to build envconf from flags: %s", err)
	}

	if os.Getenv("E2E_TEST") == "" || testing.Short() {
		os.Exit(0)
	}

	if v := os.Getenv("CLUSTER"); v != "" {
		clusterName = v
	}
	if v := os.Getenv("NAMESPACE"); v != "" {
		namespace = v
	}

	if v := os.Getenv("ACTIVE_ZONE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatal("ACTIVE_ZONE_TIMEOUT invalid")
		}
		activeZoneTimeout = d
	}

	if v := os.Getenv("STATE_CHANGE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatal("STATE_CHANGE_TIMEOUT invalid")
		}
		stateChangeTimeout = d
	}

	if v := os.Getenv("SIM_DURATION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Fatal("SIM_DURATION invalid")
		}
		simDuration = d
	}

	if v := os.Getenv("SIM_NUM_CLIENTS"); v != "" {
		d, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			log.Fatal("SIM_NUM_CLIENTS invalid")
		}
		simNumClients = int(d)
	}

	if v := os.Getenv("FEATURE_ITERATIONS"); v != "" {
		d, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			log.Fatal("FEATURE_ITERATIONS invalid")
		}
		featureIterations = int(d)
	}

	testEnv = env.NewWithConfig(cfg)
	cluster := kind.NewCluster(clusterName)

	log.Printf("Start E2E tests. clusterName=%s, namespace=%s, activeZoneTimeout=%s, stateChangeTimeout=%s, simDuration=%s, simNumClients=%d, featureIterations=%d\n",
		clusterName, namespace, activeZoneTimeout, stateChangeTimeout, simDuration, simNumClients, featureIterations)

	// Use Environment.Setup to configure pre-test setup
	testEnv.Setup(
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			log.Println("Setting up e2e testing environment...")
			return ctx, nil
		},
		envfuncs.CreateClusterWithConfig(cluster, clusterName, "kind-config.yaml", kind.WithImage(kindImage)),
		createNamespaceIfNotExist(namespace),

		// build & load docker images
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			origWd, _ := os.Getwd()

			// change dir for Makefile or it will fail
			if err := os.Chdir("../"); err != nil {
				log.Printf("Unable to set working directory: %s\n", err)
				return ctx, err
			}

			// Build docker images
			for _, dockerImage := range dockerImages {
				log.Printf("Building %s docker image...\n", dockerImage.image)
				if p := utils.RunCommand(fmt.Sprintf("docker build -f %s -t %s .", dockerImage.file, dockerImage.image)); p.Err() != nil {
					log.Printf("Failed to build %s docker image: %s: %s", dockerImage.image, p.Err(), p.Result())
					return ctx, p.Err()
				}
			}

			// Load docker images into kind
			for _, dockerImage := range dockerImages {
				log.Printf("Loading %s docker images into cluster...\n", dockerImage.image)
				if err := cluster.LoadImage(ctx, dockerImage.image); err != nil {
					log.Printf("Failed to load image %s into cluster: %s", dockerImage.image, err)
					return ctx, err
				}
			}

			if err := os.Chdir(origWd); err != nil {
				log.Printf("Unable to set working directory: %s", err)
				return ctx, err
			}

			return ctx, nil
		},

		// deploy resources
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			log.Println("Deploying common resources...")
			if p := utils.RunCommand(fmt.Sprintf("kubectl apply -n %s -f ./manifests/common/", namespace)); p.Err() != nil {
				log.Printf("Failed to deploy resources: %s: %s", p.Err(), p.Out())
				envfuncs.DeleteNamespace(namespace)
				return ctx, p.Err()
			}

			log.Println("Deploying redis...")
			if p := utils.RunCommand(fmt.Sprintf("kubectl apply -n %s -f ./manifests/redis.yaml", namespace)); p.Err() != nil {
				log.Printf("Failed to deploy redis: %s: %s", p.Err(), p.Out())
				return ctx, p.Err()
			}

			log.Println("Deploying zone-coordinator...")
			if p := utils.RunCommand(fmt.Sprintf("kubectl apply -n %s -f ./manifests/zone-coordinator.yaml", namespace)); p.Err() != nil {
				log.Printf("Failed to deploy zone-coordinator: %s: %s", p.Err(), p.Out())
				return ctx, p.Err()
			}

			if err := waitDeploymentAvailable(cfg, "zone-coordinator"); err != nil {
				return ctx, err
			}

			for _, stsName := range []string{"redis-a", "redis-b", "redis-c"} {
				if err := waitStatefulSetAvailable(ctx, cfg, stsName, 2); err != nil {
					return ctx, err
				}
			}

			for _, deployName := range []string{"election-agent-z1", "election-agent-z2", "election-agent-util"} {
				log.Printf("Deploying %s...", deployName)
				if p := utils.RunCommand(fmt.Sprintf("kubectl apply -n %s -f ./manifests/%s.yaml", namespace, deployName)); p.Err() != nil {
					log.Printf("Failed to deploy %s: %s: %s", deployName, p.Err(), p.Out())
					return ctx, p.Err()
				}
			}

			for _, deployName := range []string{"election-agent-z1", "election-agent-z2", "election-agent-util"} {
				if err := waitDeploymentAvailable(cfg, deployName); err != nil {
					return ctx, err
				}
			}

			time.Sleep(5 * time.Second)
			return ctx, nil
		},
	)

	// Use the Environment.Finish method to define clean up steps
	testEnv.Finish(
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			log.Println("Finishing tests, cleaning cluster ...")
			// utils.RunCommand(`bash -c "kustomize build config/default | kubectl delete -f -"`)
			return ctx, nil
		},
		envfuncs.DeleteNamespace(namespace),
		envfuncs.DestroyCluster(clusterName),
	)

	// launch package tests
	os.Exit(testEnv.Run(m))
}

type namespaceContextKey string

func createNamespaceIfNotExist(name string) env.Func {
	return func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
		namespace := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
		client, err := cfg.NewClient()
		if err != nil {
			return ctx, fmt.Errorf("create namespace func: %w", err)
		}

		var ns corev1.Namespace
		if err := client.Resources().Get(ctx, name, name, &ns); err != nil {
			log.Printf("The namespace %s doesn't exist, create it\n", name)
			if err := client.Resources().Create(ctx, &namespace); err != nil {
				return ctx, fmt.Errorf("create namespace func: %w", err)
			}
		} else {
			log.Printf("The namespace %s exists, skip to create namespace\n", name)
		}
		cfg.WithNamespace(name) // set env config default namespace
		return context.WithValue(ctx, namespaceContextKey(name), namespace), nil
	}
}

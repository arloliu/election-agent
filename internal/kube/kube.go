package kube

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"election-agent/internal/config"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"k8s.io/kubernetes/pkg/util/node"

	pb "election-agent/proto/election_agent/v1"

	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type KubeClient interface {
	GetPods(namespace string, deployment string) (*pb.Pods, error)
}

type kubeClient struct {
	ctx    context.Context
	cfg    *config.Config
	client *kubernetes.Clientset
}

func NewKubeClient(ctx context.Context, cfg *config.Config) (KubeClient, error) {
	var config *rest.Config
	var err error

	// when the application lives in the k8s cluster
	if cfg.Kube.InCluster {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
	} else {
		// when the application lives out of k8s cluster. e.g., on the development machine.
		// it tries to read config from the $HOME/.kube/config file
		configPath := ""
		if home := homedir.HomeDir(); home != "" {
			configPath = filepath.Join(home, ".kube", "config")
		}
		config, err = clientcmd.BuildConfigFromFlags("", configPath)
		if err != nil {
			return nil, err
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &kubeClient{ctx: ctx, cfg: cfg, client: clientset}, nil
}

func (c *kubeClient) GetPods(namespace string, deployment string) (*pb.Pods, error) {
	podsInfo := &pb.Pods{Items: make([]*pb.Pod, 0)}
	rsCache := make(map[string]*appv1.ReplicaSet)

	deploy, err := c.client.AppsV1().Deployments(namespace).Get(c.ctx, deployment, metav1.GetOptions{})
	if err != nil {
		return podsInfo, err
	}

	listOpts := metav1.ListOptions{
		LabelSelector: labels.Set(deploy.Spec.Selector.MatchLabels).String(),
	}

	pods, err := c.client.CoreV1().Pods(namespace).List(c.ctx, listOpts)
	if err != nil {
		return podsInfo, err
	}

	for _, pod := range pods.Items {
		meta := pod.ObjectMeta
		rsName := ""
		for _, ownerRef := range meta.OwnerReferences {
			if ownerRef.Kind == "ReplicaSet" {
				rsName = ownerRef.Name
				break
			}
		}
		if len(rsName) == 0 {
			return podsInfo, fmt.Errorf("failed to find a ReplicaSet in pod: %s", meta.Name)
		}

		var rs *appv1.ReplicaSet
		rs, ok := rsCache[rsName]
		if !ok {
			rs, err = c.client.AppsV1().ReplicaSets(namespace).Get(c.ctx, rsName, metav1.GetOptions{})
			if err != nil {
				return podsInfo, err
			}
			rsCache[rsName] = rs
		}

		podItem := &pb.Pod{
			Name:       meta.Name,
			Deployment: deployment,
			Ip:         pod.Status.PodIP,
			Status: &pb.PodStatus{
				Phase:           string(pod.Status.Phase),
				Reason:          pod.Status.Reason,
				Terminating:     pod.DeletionTimestamp != nil && pod.Status.Reason != node.NodeUnreachablePodReason,
				PodScheduled:    podCond(pod.Status.Conditions, corev1.PodScheduled),
				PodInitialized:  podCond(pod.Status.Conditions, corev1.PodInitialized),
				PodReady:        podCond(pod.Status.Conditions, corev1.PodReady),
				ContainersReady: podCond(pod.Status.Conditions, corev1.ContainersReady),
			},
			ReplicaSet: &pb.ReplicaSet{
				Name:                 rsName,
				Revision:             toInt32(rs.ObjectMeta.Annotations["deployment.kubernetes.io/revision"]),
				AvailableReplicas:    rs.Status.AvailableReplicas,
				FullyLabeledReplicas: rs.Status.FullyLabeledReplicas,
				ReadyReplicas:        rs.Status.ReadyReplicas,
				Replicas:             rs.Status.Replicas,
				DesiredReplicas:      toInt32(rs.ObjectMeta.Annotations["deployment.kubernetes.io/desired-replicas"]),
				MaxReplicas:          toInt32(rs.ObjectMeta.Annotations["deployment.kubernetes.io/max-replicas"]),
			},
		}

		podsInfo.Items = append(podsInfo.Items, podItem)
	}

	return podsInfo, nil
}

func podCond(conds []corev1.PodCondition, condType corev1.PodConditionType) bool {
	for _, cond := range conds {
		if cond.Type == condType {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func toInt32(s string) int32 {
	i, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0
	}
	return int32(i)
}

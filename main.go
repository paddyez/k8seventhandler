package main

import (
	"bytes"
	"fmt"
	"github.com/sergi/go-diff/diffmatchpatch"
	"golang.org/x/net/context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

var podMap = make(map[string]bool)
var sourceVersionMap = make(map[string]string)
var sourceTeamMap = make(map[string]string)

var namespace = "riplf-test-int"

//var namespace = "riplf-test-stoer-env01"

func main() {
	//repoSet := NewSet()
	// Set up signals so we handle the first shutdown signal gracefully
	stopCh := setupSignalHandler()

	config, _ := clientcmd.BuildConfigFromFlags("", "/Users/patrickpzoerner/.kube/config_kcu_riplf-test-k8s03")

	// Create a Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Error creating Kubernetes client: %v", err)
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Error retrieving pods: %v\n", err)
		os.Exit(1)
	}
	for _, pod := range pods.Items {
		if pod.Status.Phase == "Succeeded" {
			err := clientset.CoreV1().Pods(pod.Namespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
			if err != nil {
				fmt.Printf("Error deleting pod %s: %v\n", pod.Name, err)
			} else {
				fmt.Printf("Deleted pod %s\n", pod.Name)
			}
		} else {
			podMap[pod.Name] = isPodReady(pod.Status.Conditions)
			repoUriString := getRepoUriString(pod.GetAnnotations()["app/code-repo"])
			repoVersion := pod.GetAnnotations()["app/code-repo-version"]
			if repoVersion == "" {
				repoVersion = pod.GetAnnotations()["helm/chart-repo-version"]
			}
			teamName := pod.GetLabels()["app/team"]
			if repoUriString == "" || repoVersion == "" {
				fmt.Printf("Pod: %s %s %s:%s\n", pod.Name, teamName, repoUriString, repoVersion)
			}
			//repoSet.Add(repoUriString + ":" + repoVersion + "\t" + teamName)
			sourceVersionMap[repoUriString] = repoVersion
			sourceTeamMap[repoUriString] = teamName
		}
	}
	/*
		for _, element := range repoSet.Elements() {
			fmt.Println("Unique repo:", element)
		}
	*/
	healthy, unhealthy := countHealthStatus()
	fmt.Printf("Number of pods: %d (%d/%d) unique repos: %d\n", len(podMap), healthy, unhealthy, len(sourceVersionMap))

	// Set up an informer factory
	factory := informers.NewSharedInformerFactoryWithOptions(clientset, time.Second*30, informers.WithNamespace(namespace))

	// Retrieve the Pod informer from the factory
	podInformer := factory.Core().V1().Pods().Informer()
	/*
		podInformer := factory.Core().V1().Pods().InformerFor(&metav1.ListOptions{
			Namespace: namespace,
		})
	*/

	// Set up event handlers for pod creation, update, and deletion
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			if _, ok := podMap[pod.Name]; ok {
				return
			}
			podMap[pod.Name] = isPodReady(pod.Status.Conditions)
			if podMap[pod.Name] {
				klog.Infof("New pod added: %s currently %d pods.", pod.Name, len(podMap))
			} else {
				klog.Infof("\033[1;30mNew pod unready: %s currently %d pods.\033[1;37m", pod.Name, len(podMap))
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldPod := oldObj.(*corev1.Pod)
			newPod := newObj.(*corev1.Pod)
			conditionsNew := newPod.Status.Conditions
			phaseOld := oldPod.Status.Phase
			phaseNew := newPod.Status.Phase
			if !podMap[newPod.Name] {
				if isPodReady(conditionsNew) {
					podMap[newPod.Name] = true
					healthy, unhealthy := countHealthStatus()
					repoUriString := getRepoUriString(newPod.GetAnnotations()["app/code-repo"])
					initialVersion := sourceVersionMap[repoUriString]
					newVersion := newPod.GetAnnotations()["app/code-repo-version"]
					if newVersion == "" {
						newVersion = newPod.GetAnnotations()["helm/chart-repo-version"]
					}
					if (initialVersion != "" && newVersion != "") && (initialVersion != newVersion) {
						klog.Infof("\033[1;32m%s updated %s %s ⇢ %s\033[1;37m %d/%d pod: %s", newPod.GetLabels()["app/team"], repoUriString, initialVersion, newVersion, healthy, unhealthy, newPod.Name)
						sourceVersionMap[repoUriString] = newVersion
					} else {
						fmt.Printf("\033[1;30mPod meets all criteria: %s %d/%d\033[1;37m\n", newPod.Name, healthy, unhealthy)
					}
					return
				}
			}
			if phaseOld != phaseNew {
				if phaseNew == corev1.PodFailed {
					klog.Errorf("Pod Failed: %s/%s", newPod.Namespace, newPod.Name)
				} else if phaseNew == corev1.PodSucceeded {
					fmt.Printf("Pod Succeeded: %s %s/%s\n", newPod.Name, phaseOld, phaseNew)
				} else if phaseNew == corev1.PodRunning {
					//klog.Infof("Pod running still not ready: %s\n", newPod.Name)
				} else {
					fmt.Printf("Pod status changed to unhandled status: %s %s/%s\n", newPod.Name, phaseOld, phaseNew)
				}
			}
			/*
				if oldPod.ResourceVersion == newPod.ResourceVersion {
					// Periodic resync will send update events for all known Pods.
					// Two different versions of the same Pod will always have different RVs.
					return
				}
			*/
		},
		DeleteFunc: func(obj interface{}) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					klog.Errorf("Couldn't get object from tombstone %#v", obj)
					return
				}
				pod, ok = tombstone.Obj.(*corev1.Pod)
				if !ok {
					klog.Errorf("Tombstone contained object that is not a Pod %#v", obj)
					return
				}
			}
			delete(podMap, pod.Name)
			healthy, unhealthy := countHealthStatus()
			klog.Infof("\033[1;36m%s deleted: %s\033[1;37m currently %d pods %d/%d.", pod.GetLabels()["app/team"], pod.Name, len(podMap), healthy, unhealthy)
		},
	})

	// Start the informer
	factory.Start(stopCh)
	// Wait for SIGINT or SIGTERM and then stop the controller
	<-stopCh
}

func getRepoUriString(providedUri string) string {
	m := regexp.MustCompile("^git@gitlab\\.reisendeninfo\\.aws\\.db\\.de[:/]+")
	repoUriString := m.ReplaceAllString(providedUri, "")
	repoUriString = strings.Replace(repoUriString, ".git", "", 1)
	return repoUriString
}

// setupSignalHandler sets up a signal handler for SIGINT and SIGTERM and returns a channel
// that will be closed when one of these signals is received.
func setupSignalHandler() <-chan struct{} {
	stopCh := make(chan struct{})
	signalCh := make(chan os.Signal, 2)
	signal.Notify(signalCh, os.Interrupt)
	signal.Notify(signalCh, os.Kill)

	go func() {
		sig := <-signalCh
		healthy, unhealthy := countHealthStatus()
		klog.Infof("Received signal: %v pods: %d (%d/%d)", sig, len(podMap), healthy, unhealthy)
		close(stopCh)
		sig = <-signalCh
		klog.Infof("Received signal: %v pods: %d (%d/%d)", sig, len(podMap), healthy, unhealthy)
		os.Exit(1) // Second signal. Exit directly.
	}()

	return stopCh
}

func isPodReady(conditions []corev1.PodCondition) bool {
	for _, condition := range conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return arePodContainersReady(conditions)
}

func arePodContainersReady(conditions []corev1.PodCondition) bool {
	for _, condition := range conditions {
		if condition.Type == corev1.ContainersReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func countHealthStatus() (healthyCount, unhealthyCount int) {
	for _, healthy := range podMap {
		if healthy {
			healthyCount++
		} else {
			unhealthyCount++
		}
	}
	return healthyCount, unhealthyCount
}

func generatePodDiff(oldPod, newPod *corev1.Pod) string {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 0, 1, ' ', 0)

	// Compare pod specs
	specDiff := generateDiff("Spec", fmt.Sprintf("%+v", oldPod.Spec), fmt.Sprintf("%+v", newPod.Spec))
	if specDiff != "" {
		fmt.Fprintf(tw, "Spec:\n%s\n", specDiff)
	}

	// Compare pod labels
	labelsDiff := generateDiff("Labels", fmt.Sprintf("%+v", oldPod.Labels), fmt.Sprintf("%+v", newPod.Labels))
	if labelsDiff != "" {
		fmt.Fprintf(tw, "Labels:\n%s\n", labelsDiff)
	}

	// Compare pod annotations
	annotationsDiff := generateDiff("Annotations", fmt.Sprintf("%+v", oldPod.Annotations), fmt.Sprintf("%+v", newPod.Annotations))
	if annotationsDiff != "" {
		fmt.Fprintf(tw, "Annotations:\n%s\n", annotationsDiff)
	}

	// Compare pod status
	statusDiff := generateDiff("Status", fmt.Sprintf("%+v", oldPod.Status), fmt.Sprintf("%+v", newPod.Status))
	if statusDiff != "" {
		fmt.Fprintf(tw, "Status:\n%s\n", statusDiff)
	}

	tw.Flush()
	return buf.String()
}

func generateDiff(field, oldStr, newStr string) string {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(oldStr, newStr, false)
	patch := dmp.PatchMake(oldStr, diffs)
	return dmp.PatchToText(patch)
}

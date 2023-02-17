// Copyright 2023 Firefly IT Consulting Ltd.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/go-kit/log/level"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Struct that receives command line arguments.
type CLI struct {
	DryRun      bool          `short:"d" help:"If set, do not delete anything"`
	GracePeriod time.Duration `short:"g" help:"Additional grace period added to that of the pod in Go duration syntax, e.g 2m, 1h etc." default:"${default_grace}"`
	Interval    time.Duration `short:"i" help:"Interval between scans of the cluster in Go duration syntax, e.g 2m, 1h etc." default:"${default_interval}"`
	Kubeconfig  string        `short:"k" help:"Specify a kubeconfig for authentication. If not set, then in cluster authentication is attempted"`
	LogLevel    string        `short:"l" help:"Sets the loglevel. Valid levels are debug, info, warn, error" default:"${default_level}"`
	LogFormat   string        `short:"f" help:"Sets the log format. Valid formats are json and logfmt" default:"${default_format}"`
	LogOutput   string        `short:"o" help:"Sets the log output. Valid outputs are stdout and stderr" default:"${default_output}"`
}

// goroutine that waits for any of the nomiated signals to be raised.
// Pushes into a channel being monitored by func signalRaised() and exits when a signal is detected.
func signalHandler(cli CLI, sigs chan os.Signal, done chan bool) {

	logger := getLogger(cli.LogLevel, cli.LogOutput, cli.LogFormat)
	sig := <-sigs

	_ = level.Info(logger).Log("message", fmt.Sprintf("INFO: Signal received: %v", sig))
	done <- true
}

// Checks to see if a signal has been raised indicating we should shut down.
func signalRaised(raised chan bool) bool {
	select {
	case _, ok := <-raised:
		if ok {
			// Signal raised, exit.
			return true
		}
	default:
		// Do nothing
		break
	}

	return false
}

// Test if a pod is static. Static pods are owned by nodes.
func isStaticPod(pod *v1.Pod) bool {
	for _, o := range pod.ObjectMeta.GetOwnerReferences() {
		if o.Kind == "Node" {
			return true
		}
	}

	return false
}

// Check whether a pod is stuck in Terminating. Force delete if it is.
// Returns false if we should shut down.
func processPod(cli CLI, clientset *kubernetes.Clientset, namespace string, pod *v1.Pod, done chan bool) bool {

	logger := getLogger(cli.LogLevel, cli.LogOutput, cli.LogFormat)
	podName := pod.ObjectMeta.Name

	if signalRaised(done) {
		return false
	}

	pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})

	if err != nil {
		_ = level.Error(logger).Log("message", fmt.Sprintf("Cannot get pod %s in namespace %s: %s", podName, namespace, err.Error()))
		return true
	}

	// Check the state of the pod
	now := time.Now()
	deletionTimestamp := pod.ObjectMeta.DeletionTimestamp

	if deletionTimestamp == nil {
		// Not been terminated
		return true
	}

	// If pod is owned by a node, then it's static and should not be deleted this way.
	if isStaticPod(pod) {
		_ = level.Warn(logger).Log("message", fmt.Sprintf("Cannot terminate static pod %s in namespace %s", podName, namespace))
		return true
	}

	// Total grace period allowed for termination. Pod's grace period + any set by command line
	syntheticGracePeriod := (time.Duration(*pod.Spec.TerminationGracePeriodSeconds) * time.Second) + cli.GracePeriod

	// Current time minus the grace period
	deleteBy := metav1.Time{Time: now.Add(-syntheticGracePeriod)}

	if deletionTimestamp.Before(&deleteBy) {
		//
		terminatingDuration := now.Sub(deletionTimestamp.Time).Round(time.Second)
		_ = level.Warn(logger).Log("message", fmt.Sprintf("Pod %s in namespace %s has been terminating for %v, which exceeds grace period of %v. Force deleting...", podName, namespace, terminatingDuration, syntheticGracePeriod))

		if cli.DryRun {
			_ = level.Warn(logger).Log("message", fmt.Sprintf("Pod %s in namespace %s would be force deleted", podName, namespace))
		} else {
			gracePeriodSeconds := int64(0)
			err = clientset.CoreV1().Pods(namespace).Delete(context.TODO(), podName, metav1.DeleteOptions{
				GracePeriodSeconds: &gracePeriodSeconds,
			})

			if err != nil {
				_ = level.Error(logger).Log("message", fmt.Sprintf("Cannot force delete pod %s in namespace %s: %s", podName, namespace, err.Error()))
			} else {
				_ = level.Warn(logger).Log("message", fmt.Sprintf("Pod %s in namespace %s has been force deleted", podName, namespace))
			}
		}
	}

	return true
}

// Iterate through all namespaces, checking pod states.
// Returns false if we should shut down.
func processNamespaces(cli CLI, clientset *kubernetes.Clientset, done chan bool) bool {

	logger := getLogger(cli.LogLevel, cli.LogOutput, cli.LogFormat)
	namespaces, err := clientset.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})

	if err != nil {
		_ = level.Error(logger).Log("message", fmt.Sprintf("ERROR: Cannot list namespaces: %s", err.Error()))
		return true
	}

	for _, ns := range namespaces.Items {

		namespace := ns.ObjectMeta.Name
		pods, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})

		if err != nil {
			_ = level.Error(logger).Log("message", fmt.Sprintf("ERROR: Cannot list pods in namespace %s: %s", namespace, err))
			continue
		}

		for _, pod := range pods.Items {
			if !processPod(cli, clientset, namespace, &pod, done) {
				return false
			}
		}
	}

	return true
}

// Main control loop. Iterate all pods in all namespaces and check their state.
func controlLoop(cli CLI, clientset *kubernetes.Clientset) {

	logger := getLogger(cli.LogLevel, cli.LogOutput, cli.LogFormat)

	// Channel to receive OS signals
	sigs := make(chan os.Signal, 1)

	// Channel to indicate signal raised
	done := make(chan bool, 1)

	// Set signals to listen for
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Start signal listener
	go signalHandler(cli, sigs, done)

	// Main loop
	for {
		_ = level.Info(logger).Log("message", "Checking for terminating pods")
		if !processNamespaces(cli, clientset, done) {
			return
		}

		// Sleep, whilst checking for signals
		select {
		case <-done:
			// finished
			return
		case <-time.After(cli.Interval):
			// Sart next iteration
			continue
		}
	}
}

func main() {

	var cli CLI

	kong.Parse(&cli,
		kong.Vars{
			"default_interval": "5m",
			"default_grace":    "1h",
			"default_level":    "info",
			"default_format":   "logfmt",
			"default_output":   "stdout",
		})

	var err error
	var config *rest.Config

	logger := getLogger(cli.LogLevel, cli.LogOutput, cli.LogFormat)

	if len(cli.Kubeconfig) > 0 {
		// Use kubeconfig passed on command line
		_ = level.Info(logger).Log("message", "Loading kubeconfig")
		config, err = clientcmd.BuildConfigFromFlags("", cli.Kubeconfig)

		if err != nil {
			_ = level.Error(logger).Log("message", fmt.Sprintf("Failed to authenticate via kubeconfig: %s", err.Error()))
			os.Exit(1)
		}
	} else {
		_ = level.Info(logger).Log("message", "Checking for service account token")
		os.Setenv("KUBERNETES_SERVICE_HOST", "kubernetes.default.svc")
		os.Setenv("KUBERNETES_SERVICE_PORT", "443")
		config, err = rest.InClusterConfig()

		if err != nil {
			_ = level.Error(logger).Log("message", fmt.Sprintf("Failed to authenticate in-cluster: %s", err.Error()))
			os.Exit(1)
		}
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)

	if err != nil {
		_ = level.Error(logger).Log("message", fmt.Sprintf("ERROR cannot create clientset: %s", err.Error()))
		os.Exit(1)
	}

	controlLoop(cli, clientset)
	_ = level.Info(logger).Log("message", "Shutting down")
}

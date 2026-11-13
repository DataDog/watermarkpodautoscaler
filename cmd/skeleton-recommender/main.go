// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2024 Datadog, Inc.

package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/spf13/pflag"
	"google.golang.org/protobuf/encoding/protojson"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/DataDog/agent-payload/v5/autoscaling/kubernetes"
)

// Static recommendation. The bounds must not follow the current replica count, or a converging WPA never stops.
var (
	targetReplicas     int32
	lowerBoundReplicas int32
	upperBoundReplicas int32
)

func int32Ptr(i int32) *int32 {
	return &i
}

// Static autoscaler that always recommends the same number of replicas, can be tried with
// curl -X POST -d '{"state": {"currentReplicas": 1}, "targets": [{"type": "cpu", "targetValue": 0.5}]}' http://localhost:8089/autoscaling
func autoscaling(w http.ResponseWriter, r *http.Request) {
	log.Printf("Handling %s %s %s bytes\n", r.Method, r.URL.Path, r.Header.Get("Content-Length"))

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return
	}

	request := &kubernetes.WorkloadRecommendationRequest{}
	err = protojson.Unmarshal(body, request)
	if err != nil {
		http.Error(w, "Failed to unmarshal request", http.StatusInternalServerError)
		return
	}

	log.Printf("< %+v\n", request)

	var currentReplicas = int32(0)

	if request.GetState() != nil {
		currentReplicas = request.GetState().GetCurrentReplicas()
	}

	if currentReplicas == 0 {
		http.Error(w, "Current replicas is 0 or unknown, disabling autoscaling.", http.StatusBadRequest)
		return
	}

	response := &kubernetes.WorkloadRecommendationReply{
		TargetReplicas:     targetReplicas,
		LowerBoundReplicas: int32Ptr(lowerBoundReplicas),
		UpperBoundReplicas: int32Ptr(upperBoundReplicas),
		Reason:             fmt.Sprintf("Autoscaling from %d to %d replicas", currentReplicas, targetReplicas),
		ObservedTargets:    request.GetTargets(),
	}

	data, err := protojson.Marshal(response)
	if err != nil {
		http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(data)
	if err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
		return
	}

	log.Printf("> %+v\n\n", response)
}

// Redirects /autoscaling/redirect to /autoscaling
func autoscalingRedirect(w http.ResponseWriter, r *http.Request) {
	log.Printf("Redirecting %s to /autoscaling", r.URL.Path)
	http.Redirect(w, r, "/autoscaling", http.StatusPermanentRedirect)
}

func main() {
	flags := pflag.NewFlagSet("skeleton-recommender", pflag.ExitOnError)
	flags.String("addr", ":8089", "Address to listen on")
	flags.Int32Var(&targetReplicas, "replicas", 3, "Recommended number of replicas")
	flags.Int32Var(&lowerBoundReplicas, "lower-bound", 2, "Fewest replicas that keep the usage under the high watermark")
	flags.Int32Var(&upperBoundReplicas, "upper-bound", 5, "Most replicas that keep the usage above the low watermark")

	pflag.CommandLine = flags
	_ = flags.Parse(os.Args[1:]) // exits on error (pflag.ExitOnError)
	if lowerBoundReplicas > targetReplicas || targetReplicas > upperBoundReplicas {
		log.Fatalf("--lower-bound (%d) <= --replicas (%d) <= --upper-bound (%d) is required", lowerBoundReplicas, targetReplicas, upperBoundReplicas)
	}

	http.HandleFunc("/autoscaling", autoscaling)
	http.HandleFunc("/autoscaling/redirect", autoscalingRedirect)

	err := http.ListenAndServe(flags.Lookup("addr").Value.String(), nil)
	if err != nil {
		panic(err)
	}
}

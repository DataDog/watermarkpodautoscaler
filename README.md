# Watermark Pod Autoscaler Controller

## Overview

The Watermark Pod Autoscaler (WPA) Controller is a custom controller that extends the Horizontal Pod Autoscaler or HPA.

### The features

- Set a high and a low bound to prevent autoscaling events.
- Specify the velocity of scaling.
- Specify windows of time to restrict upscale or downscale events.
- Different algorithms to compute the desired number of replicas.

### The Goal

This project is meant to solve the limitations faced internally with the upstream pod autoscaler controller.
Many teams are undergoing a lot of manual efforts to autoscale but can't rely on the HPA as the logic is too simple.
We believe that many of the features of the WPA controller, if not all, should be in the HPA.
While we do use the WPA internally, we also want to submit a [KEP](https://github.com/kubernetes/enhancements/tree/master/keps) to suggest upstreaming the features and making them available to the community.

### When to use it

If you want to autoscale some of your applications but:
- The single threshold logic of the HPA is not enough.
- You need to specify forbidden windows specific to your application.
- You want to limit the velocity of scaling.

The limitations of the HPA are the basis of the WPA.

## Usage

### The algorithm

There are two options to compute the desired number of replicas. Depending on your use case, you might want to consider one or the other.

1. `average`
    The ratio computed is `value from the external metrics provider` / `current number of replicas`, it is compared to the watermarks and the recommended number of replicas is `value from the external metrics provider` / `watermark` (low or high depending on the current value).
    - The `average` algorithm is a good fit if you use a metric that does not depend on the number of replicas. Typically, the number of requests received by an ELB can indicate how many webservers we want to have given that we know that a single webserver should handle X rq/s.
    Adding a replica will not increase/decrease the # of requests received.

2. `absolute`
    The default value is `absolute`, we compare the raw **avg** metric gotten from the external metrics provider and it is considered the utilization ratio and the recommended number of replicas is computed as `current number of replicas` * `value from the external metrics provider` / `watermark`.
    - The `absolute` algorithm is the default as it represents the most common use case: You want your application to run between 60% and 80% of CPU, if avg:cpu.usage is at 85%, you need to scale up. The metric has to be correlated to the # of replicas.

Worth noting: In the upstream controller only the `math.Ceil` function is used to round up the recommended number of replicas.
This means that if you have a threshold at 10, you will need to reach a utilization of 8.999... from the external metrics provider to downscale by one replica but 10.001 will make you go up 1 replicas.
The WPA controller will use `math.Floor` if the value is under the Lower WaterMark, this ensures a symetric behavior. Which, on top of the other scaling options allows finer control over when to downscale.

### The process

Create your [WPA](https://github.com/DataDog/watermarkpodautoscaler/blob/master/deploy/crds/datadoghq_v1alpha1_watermarkpodautoscaler_cr.yaml) in the same namespace as your target deployment, then create an HPA targeting a deployment that does not exist, but configure the same metric as in the WPA. This is because I have not implemented the informer on the DCA side to watch for WPAs.

The Datadog Cluster Agent will pick up the Creation/Update/Deletion event and parse through the Spec of the WPA to extract the metric and scope to get from Datadog.

### Concrete examples

In this example we are using the following Spec configuration:
```
    downscaleForbiddenWindowSeconds: 60
    upscaleForbiddenWindowSeconds: 30
    scaleDownLimitFactor: 30
    scaleUpLimitFactor: 50
    minReplicas: 4
    maxReplicas: 9
    metrics:
    - external:
        highWatermark: 400m
        lowWatermark: 150m
        metricName: custom.request_duration.max
        metricSelector:
          matchLabels:
            kubernetes_cluster: mycluster
            service: billing
            short_image: billing-app
      type: External
    tolerance: 0.01
```

* **Bounds**

Starting with the watermarks, the value of the metric collected (`watermarkpodautoscaler.wpa_controller_value`) from Datadog in purple when between the bounds (`watermarkpodautoscaler.wpa_controller_low_watermark` and `watermarkpodautoscaler.wpa_controller_high_watermark`) will instruct the controller not to trigger a scaling event. They are specified as `Quantities` so you can use `m | "" | k | M | G | T | P | E` to easily represent the value you want to use.

We can use the metric `watermarkpodautoscaler.wpa_controller_restricted_scaling{reason:within_bounds}` to verify that it is indeed restricted. (Nota: the metric was multiplied by 1000 in order to make it look more explicit that during this time, no scaling event could have been triggered by the controller).
<img width="1528" alt="Within Watermarks" src="https://user-images.githubusercontent.com/7433560/63385633-e1a67400-c390-11e9-8fee-c547f1876540.png">

* **Velocity**

The second set of configuration options pertains to the scaling velocity of your deployment, controlled by `scaleDownLimitFactor` and `scaleUpLimitFactor`.
They are integers between 0 and 100 and represent the maximum ratio of respectively downscaling and upscaling given the current number of replicas.

In this case, should we have 10 replicas and a recommended number of replicas at 14 (see the [Algorithm section](#the-algorithm) for more details on the recommendation) with a scaleUpFactor of 30 (%), we would be capped at 13 replicas.

In the following graph, we can see that the suggested number of replicas (in purple), represented by the metric `watermarkpodautoscaler.wpa_controller_replicas_scaling_proposal` is too high compared to the current number of replicas, this will trigger the upscale capping logic, which can be monitored using the metric `watermarkpodautoscaler.wpa_controller_restricted_scaling{reason:upscale_capping}` (Nota: Same as above, the metric was multiplied to make it more explicit). Thus, the effective number of replicas `watermarkpodautoscaler.wpa_controller_replicas_scaling_effective` will scale up, but according to the `scaleUpLimitFactor`
<img width="911" alt="Upscale Capping" src="https://user-images.githubusercontent.com/7433560/63385168-f46c7900-c38f-11e9-9e7c-1a7796afd31e.png">

In this similar example, we avoid downscaling too much, and can use the same set of metrics to guarantee that we only scale down by a reasonable amount of replicas.
<img width="911" alt="Downscale Capping" src="https://user-images.githubusercontent.com/7433560/63385340-44e3d680-c390-11e9-91d4-35b2f8a912ad.png">

It is important to note that we always make conservative scaling decision.
- with a `scaleUpLimitFactor` of 29% if we have 10 replicas and are recommended 13 we will upscale to 12.
- with a `scaleDownLimitFactor` of 29% if we have 10 replicas and are recommended 7 we will downscale to 8.
- The minimum amount of replicas we can recommend to add or remove is 1 (not 0), this is to avoid edge scenarii when using a small number of replicas
- It is important to keep in mind that the options `minReplicas` and `maxReplicas` take precedence. As per the ##Precedence paragraph

* **Cooldown periods**

Finally the last options we want to use are `downscaleForbiddenWindowSeconds` and `upscaleForbiddenWindowSeconds` in seconds that represent respectively how much time we wait before we can respectively scale down and scale up after a **scaling event**. We only keep the last scaling event, and we do not compare the `upscaleForbiddenWindowSeconds` to the last time we only upscaled.

In the following example we can see that the recommended number of replicas is ignored if we are in a cooldown period. The downscale cooldown period can me visualised with `watermarkpodautoscaler.wpa_controller_transition_countdown{transition:downscale}`, and is represented in yellow on the graph below. We can see that it is significantly higher than the upscale cooldown period (`transition:upscale`) in orange on our graph. As soon as we are recommended to scale, only if the appropriate cooldown window is over, will we scale. This will reset both countdowns.
<img width="911" alt="Forbidden Windows" src="https://user-images.githubusercontent.com/7433560/63389864-a14cf300-c39c-11e9-9ad5-8308af5442ad.png">

* **Precedence**

Essentially, as we retrieve the value of the External Metric, we will first compare it to the `highWatermark` + `tolerance` and `lowWatermark` - `tolerance`.
If we are outside of the bounds, we compute the recommended number of replicas.
Then we compare this value to the current number of replicas to potentially cap the recommended number of replicas.
Finally, we look at if we are allowed to scale given the `downscaleForbiddenWindowSeconds` and `upscaleForbiddenWindowSeconds`.

## Limitations

- Only for External Metrics.
- Only officially supports 1 metric per WPA.
- Does not take the CPU into account to normalize the number of replicas.
- Does not consider the readiness of pods in the targeted deployment.
- Similar to the HPA, the controller polls the External Metrics Provider every 15 seconds, which refreshes metrics every 30 seconds.

## Troubleshooting

#### Lifecycle

In addition to the metrics mentioned above, these are logs that will help you better understand the proper functioning of the WPA.

Every 15 seconds, we retrieve the metric listed in the `metrics` section of the spec.

```
{"level":"info","ts":1566327479.866722,"logger":"wpa_controller","msg":"Target deploy: {datadog/propjoe-green replicas:6}"}
{"level":"info","ts":1566327479.8844478,"logger":"wpa_controller","msg":"Metrics from the External Metrics Provider: [127]"}
{"level":"info","ts":1566327479.8844907,"logger":"wpa_controller","msg":"About to compare utilization 127 vs LWM 150 and HWM 400"}
{"level":"info","ts":1566327479.8844962,"logger":"wpa_controller","msg":"Value is below lowMark. Usage: 127 ReplicaCount 5"}
{"level":"info","ts":1566327479.8845394,"logger":"wpa_controller","msg":"Proposing 5 replicas: Based on dd.propjoe.request_duration.max{map[kubernetes_cluster:barbet service:propjoe short_image:propjoe]} at 18:57:59 targeting: Deployment/datadog/propjoe-green"}
```

We see the current number of replicas seen in the target deployment, here 6.
Then we see the raw value retrieved from the External Metrics Provider and then we compare it as a Quantity to the high and low watermarks.
Given the result of this comparison, we print the recommended number of replicas, here 5.

If you want to query the External Metrics Provider directly you can use the following command:

`➜ kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/<namespace of your deployment>/<name of the metrics>" | jq .`

you can optionally add label selectors too by adding `?labelSelector=key%3Dvalue`.
If we wanted to retrieve our metric in this case, we could use:

`➜ kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/datadog/dd.propjoe.request_duration.max?labelSelector=kubernetes_cluster%3Dbarbet%2Cservice%3Dpropjoe%2Cshort_image%3Dpropjoe" | jq .`

If you see logs such as:
```
{"level":"info","ts":1566397216.8918724,"logger":"wpa_controller","msg":"failed to compute desired number of replicas based on listed metrics for Deployment/datadog/propjoe-green: failed to get external metric dd.propjoe.request_duration.max: unable to get external metric datadog/propjoe-green/&LabelSelector{MatchLabels:map[string]string{fooa: bar,},MatchExpressions:[],}: no metrics returned from external metrics API"}
```

You can verify that this metric is indeed not available from the External Metrics Provider, which could be due to a typo in the labels, or the metric can't be fetched from Datadog (which could be due to various factors: too sparse, API down, rate limit hit...).
You can go in the logs of the External Metrics Provider to further investigate (Deployment datadog-agent/datadog-cluster-agent, or ping #k8s-dd-agent).

Then we go through the Scaling velocity capping and the cooldown windows verifications.
In the case of a scaling capping you would see something like:
```
{"level":"info","ts":1566327268.8839815,"logger":"wpa_controller","msg":"Upscaling rate higher than limit of 50.0% up to 9 replicas. Capping the maximum upscale to 9 replicas"}
{"level":"info","ts":1566327268.884001,"logger":"wpa_controller","msg":"Returning 9 replicas, condition: ScaleUpLimit reason the desired replica count is increasing faster than the maximum scale rate"}
{"level":"info","ts":1566327479.8845513,"logger":"wpa_controller","msg":" -> after normalization: 9"}
```

Then we consider the cooldown periods. You will have logs indicative of when the last scaling event was and until when the next upscale and downscale events are forbidden:
```
{"level":"info","ts":1566327479.8845847,"logger":"wpa_controller","msg":"Too early to downscale. Last scale was at 2019-08-20 18:57:44 +0000 UTC, next downscale will be at 2019-08-20 18:58:44 +0000 UTC, last metrics timestamp: 2019-08-20 18:57:59 +0000 UTC"}
{"level":"info","ts":1566327479.8846018,"logger":"wpa_controller","msg":"Too early to upscale. Last scale was at 2019-08-20 18:57:44 +0000 UTC, next upscale will be at 2019-08-20 18:58:14 +0000 UTC, last metrics timestamp: 2019-08-20 18:57:59 +0000 UTC"}
{"level":"info","ts":1566327479.884608,"logger":"wpa_controller","msg":"backoffUp: true, backoffDown: true, desiredReplicas 5, currentReplicas: 6"}
```

Finally, closing the loop we have a verification that the deployment was correctly autoscaled:
```
{"level":"info","ts":1566327253.7887673,"logger":"wpa_controller","msg":"Successful rescale of watermarkpodautoscaler-propjoe, old size: 8, new size: 9, reason: dd.propjoe.request_duration.max{map[kubernetes_cluster:barbet service:propjoe short_image:propjoe]} above target"}
```

#### FAQ:

- What happens if I scale manually my deployment ?  
    In the next reconcile loop, the new number of replicas will be considered to compute the desired number of replicas. You might see a log saying that the resource was modified by someone else. If the number of replicas configured is outside of the bounds however the controller will scale this back to a number of replicas within the acceptable range.

- What is the footprint of the controller ?  
    From our testing, it is a factor of the number of deployments in the cluster. 
    Barbet: 500+ deployments, 65MB - 10mCores
    Chinook: 1600+ deployments, 105MB - 5mCores
    **Worth noting:** When the APIServer restart, the controller-runtime caches the old state and the new one for a second and then merges everything. This makes the memory usage shoot up and can OOM the controller.

- Is the Controller Stateless ?  
    Yes.

#### RBAC:

As we watch all the WPA definitions cluster wide, we use a clusterrole.
A useful option is to impersonate the user to verify rights, so for instance you can verify that you have the right to get a deployment as the WPA controller's service account:
`➜ kubectl get deploy logs-index-router-logs-datadog  --as system:serviceaccount:datadog:watermarkpodautoscaler -n logs-storage`

Or query the External Metrics Provider:

`➜ kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/datadog/metric --as system:serviceaccount:datadog:watermarkpodautoscaler`


## Observability

While this is still a WIP as we set in place the best signals to monitor both the controller's health and it's behavior a few dashboards have been created which could give you an idea of what is good to measure to make sure everything is working as expected:

- [Controller's Health and Overview in Staging](https://ddstaging.datadoghq.com/dashboard/2sz-ti4-yhz/watermark-pod-autoscaler-controller)
- [Propjoe in Barbet [EU/Staging]](https://app.datad0g.eu/dashboard/6ur-73s-ne5/propjoe-autoscaling)
- Logs-Index-Router [US/Staging]

## Developer guide

### setup your dev environment

Requirements:

* golang >= 1.12
* make
* docker
* git

After cloning the repository: `https://github.com/DataDog/watermarkpodautoscaler`, you need to set some environment variables

```console
export GO111MODULE=on
unset GOPATH
export PATH=$PATH:$(pwd)/bin
```

then, to install some tooling dependencies, you need to execute: `make install-tools`

### useful commands

* `make build`: build locally the controller
* `make generate`: run the several operator-sdk generator
* `make test`: run unit-tests
* `make validate`: run common golang linters (`golangci-lint`)
* `make e2e`: run end 2 end test on the current kubernetes cluster configured.
* `make container`: build the controller docker image using the `operator-sdk`
* `make container-ci`: build the controller docker image with the multi-stage Dockerfile

## Acknowledgement

Some of the features were inspired by the [Configurable HPA](https://github.com/postmates/configurable-hpa) or CHPA.
Most of the code structure was also used for the Watermark Pod Autoscaler, although the overall packaging of the CRD was done with the operator-sdk.

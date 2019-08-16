# Watermark Pod Autoscaler

## 30'000 ft overview

The Watermark Pod Autoscaler or WPA is a custom controller that extends the Horizontal Pod Autoscaler or HPA.

## The features

- Set a high and a low bound to restrict the autoscaling.
- Specify the velocity of scaling.
- Specify windows of time to restrict upscale or downscale events.
- Different algorithms to compute the desired number of replicas.

## Acknowledgement

Some of the features were inspired by the [Configurable HPA](https://github.com/postmates/configurable-hpa) or CHPA.
The code structure was also used for the Watermark Pod Autoscaler, although the overal packaging of the CRD was done with the operator-sdk.

## The Goal

This project is meant to solve the limitations faced internally with the upstream pod autoscaler controller.
As we use it locally we are also going to submit a KEP to hopefully get its features available in the upstream controller.

## Limitations

- Only for External Metrics.
- Only officially supports 1 metric per WPA.
- Does not take the CPU into account to normalize the number of replicas.
- Does not consider the readiness of pods in the targeted deployment.

## How to use it


## The algorithm

There are two options to compute the desired number of replicas.
Depending on your use case, you might want to consider one or the other.
1. `average`
    If
2. `absolute`


## Developer guide

### setup your dev environment

Requirements:

* golang >= 1.12
* make
* docker
* git

After cloning the repository: `https://github.com/DataDog/watermarkpodautoscaler`, you need to set some environement variables

```console
export GO111MODULE=on
unset GOPATH
export PATH=$PATH:$(pwd)/bin
```

then, to install some tooling dependencies, you need to execute: `make install-tools`

### useful commands

* `make build`: build localy the controller
* `make generate`: run the several operator-sdk generator
* `make test`: run unit-tests
* `make validate`: run common golang linters (`golangci-lint`)
* `make e2e`: run end 2 end test on the current kubernetes cluster configured.
* `make container`: build the controller docker image using the `operator-sdk`
* `make container-ci`: build the controller docker image with the multi-stage Dockerfile

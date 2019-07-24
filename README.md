# WatermarkPodAutoscaler

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

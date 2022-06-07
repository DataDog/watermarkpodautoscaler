# Kubectl WPA plugin

The WatermarkPodAutoscaler Controler comes with a kubectl plugin providing a set of helper utilities, like changing dry-run configuration.

## Installation 

### With krew

#### From github release artifact

this solution is interesting to also use release condidate version.

```console
export VERSION=0.4.0
k krew install --manifest-url https://github.com/DataDog/watermarkpodautoscaler/releases/download/$VERSION/wpa-plugin.yaml` 
```

### From krew public index

:warning: the `wpa` plugin is not yet available inthe krew index

To install, use the [krew plugin manager](https://krew.sigs.k8s.io/).

```console
$ kubectl krew install wpa
Installing plugin: wpa
Installed plugin: wpa
\
 | Use this plugin:
 | 	kubectl wpa
 | Documentation:
 | 	https://github.com/DataDog/watermarkpodautoscaler
/
```


### From source

```console
$ make kubectl-wpa
go fmt ./...
go vet ./...
./bin/golangci-lint run ./...
CGO_ENABLED=1 go build -ldflags '-w -s -o bin/kubectl-wpa ./cmd/kubectl-wpa/main.go

$ kubectl wpa --help
Usage:
  kubectl [command]

Available Commands:
  dry-run     configure WPA(s) dry-run
  help        Help about any command
```

## Usage

```console
➜ kubectl wpa --help
Usage:
  kubectl [command]

Available Commands:
  dry-run     configure WPA(s) dry-run
  help        Help about any command
```

```
➜ kubectl wpa dry-run  --help 
configure WPA(s) dry-run

Usage:
  kubectl dry-run [flags]
  kubectl dry-run [command]

Available Commands:
  disable     disable WPA(s) dry-run mode
  enable      enable WPA(s) dry-run mode
  list        list dry-run mode of wpa(s)
  revert      revert all WPA instance dry-run configuration from a csv backup file
```

### Examples

#### list current dry-run WPA states

```console
$ kubectl wpa dry-run list -n bar foo
WatermarkPodAutoscaler 'bar/foo' dry-run option is: true
$ kubectl wpa dry-run list -n bar -l app=foo
WatermarkPodAutoscaler 'bar/foo' dry-run option is: true
WatermarkPodAutoscaler 'bar/foo2' dry-run option is: true
$ kubectl wpa dry-run list --all -n ""
WatermarkPodAutoscaler 'bar/foo' dry-run option is: true
WatermarkPodAutoscaler 'bar/foo2' dry-run option is: true
WatermarkPodAutoscaler 'default/test' dry-run option is: false
```

#### enabled/disable dry-run

enable "dry-run" on a specific WPA instance

```console
$ kubectl wpa dry-run enable -n bar foo
```

enable "dry-run" on WPA instance(s) matching a label selector

```console
$ kubectl wpa dry-run enable -l app=foo
```

enable "dry-run" on all WPA instance(s)

```console
$ kubectl wpa dry-run enable --all
```

### how to backup and revert WPA instances dry-run configuration

```console
$ kubectl wpa dry-run list -n "" --all -o csv > wpa-backup.csv
$ cat wpa-backup.csv
default,example1-watermarkpodautoscaler,disabled
default,example2-watermarkpodautoscaler,disabled
default,example3-watermarkpodautoscaler,disabled
$ cat wpa-backup.csv | kubectl wpa dry-run revert
# or
$ kubectl wpa dry-run revert -f wpa-backup.csv

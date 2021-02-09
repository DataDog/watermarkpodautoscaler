# Tests

## Unit-tests

The command: ```make test``` will execute the unit-tests in every package and also generate the code coverage report.

## End to end testing

End to end test suite can be executed with the comment: ```make e2e```.

To test locally, you should use "[Kind](https://kind.sigs.k8s.io/)" for creating a multi nodes local cluster.
And use the Kind cluster template: `test/cluster-kind.yaml`

```console
kind create cluster --config test/cluster-config-gitlabci.yaml
```

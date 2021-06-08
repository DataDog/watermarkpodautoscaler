# Release process

## Steps

- Checkout the repository on the correct branch (`main`)
- Run locally the command `make VERSION=x.y.z release`
- Commit all the changes generated from the previous command:
    ```console
    $ git add .
    $ git commit -s -m "release vX.Y.Z"
    ```
- Add the release tag:
    ```console
    $ git tag vX.Y.Z
    ```
- Push it:
    ```console
    $ git push origin vX.Y.Z
    ```

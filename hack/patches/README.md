### Overview
Periodically bug fixes will be needed in upstream code that this project depends upon.
Rather than patch vendored code, this directory provides a place to isolate individual patches that may be applied at build time.
**For now only a single HUNK should be present in each `.diff` file.**

**NOTE:** this is not intended to replace proper pull-requests in other projects.
Rather it is a short-term stop-gap solution for moving forward with patched code until the appropriate PR's are applied upstream and this project has been rebased to a revision that includes said PR's.

### Naming Convention
Patch files are named according to the upstream project they apply to and the issue number relative to **this project**.
```
  {patched-project}---{k8s-mesos-issue}.diff
```

For example, a file named `k8s---issue1234.diff` would indicate a patch for the Kubernetes project, tracked by issue 1234 in this project's (kubernetes-mesos) issues list.
Issue 1234 should cross-reference any relevant PR's in the upstream project's repository.

#### Projects

Project Code | Project Name
-------------|-------------
 k8s         | [kubernetes](https://github.com/GoogleCloudPlatform/kubernetes)

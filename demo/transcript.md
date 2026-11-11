# Demo transcript: demo

## 00:00  Architecture

This is a small production-style platform on GKE Autopilot. A Go microservice called url-health-api registers URLs, probes them every minute and exposes Prometheus metrics. Every cloud resource is Terraform: the VPC with Cloud NAT, the private Autopilot cluster, Artifact Registry, Workload Identity, Firestore, and the monitoring SLOs. Delivery is GitOps: GitHub Actions builds and pushes the image with keyless auth, and ArgoCD inside the cluster pulls the manifests and keeps the cluster in sync.

## 00:35  API

First the running service, through the global external load balancer that the Gateway API created. I register two URLs, one healthy and one that returns 404, and a bad one that fails validation. A minute later the checker has probed both: Google's site is up with its latency, the broken one is down with the 404. The data lives in Firestore, written by the pod through Workload Identity.

## 01:01  Identity

Here is the identity chain. The Kubernetes service account is annotated with the Google service account, the binding grants workload identity user on that account only, and the Google account itself has a single role: datastore user. There are no keys on it. The CI identity is similar: a workload identity pool trusts GitHub's OIDC issuer, restricted to this one repository, and maps to a service account that can only write to Artifact Registry. Nothing in this pipeline stores a credential.

## 01:33  GitOps push

Now the GitOps loop, live. I change the service, in this case the version endpoint, and push to main. GitHub Actions runs the tests, builds the image, pushes it to Artifact Registry using the federated token, and then commits the new image tag into the kustomization file. That commit is the deployment.

## 01:52  GitHub Actions

This is the workflow run: the test job, then build and push. The auth step shows the workload identity exchange, no JSON key anywhere. The last step bumps the tag in k8s slash base and pushes it back to the repository.

## 02:15  ArgoCD

ArgoCD watches that path. Once the tag commit lands, the application shows out of sync for a moment, then the automated sync rolls the deployment: a new ReplicaSet with the new image, surge one, zero unavailable, and the old pods drain after the readiness probe passes. Self-heal and prune are on, so manual drift is reverted and deleted manifests are removed.

## 02:45  Rollout

Back on the command line, the deployment now runs the image tagged with the new commit, and the version endpoint returns it through the load balancer. From git push to a live rollout, no one touched kubectl.

## 03:00  SLOs

Observability is Google Managed Prometheus scraping the pod's metrics. On top of that, Cloud Monitoring has a custom service with two SLOs: ninety-nine percent of checks requests under 300 milliseconds, and ninety-nine point five percent non-5xx. The alert policy is a multi-window burn-rate alert, fourteen-point-four x over one hour or six x over six hours, which pages on sustained budget burn rather than single blips.

## 03:30  Security

The security checklist, verified: private nodes with Cloud NAT, the control plane reachable only from one authorized IP, pods non-root with a read-only root filesystem and all capabilities dropped, a default-deny network policy with explicit allows for the load balancer, the Prometheus collector, DNS and outbound HTTPS, and a distroless image.

## 03:54  Wrap-up

Everything shown is in the repository: the Go service with tests, Terraform for the platform, kustomize manifests, the ArgoCD application and the CI workflow. Autopilot bills per pod, so the whole platform costs a few rupees an hour while it runs, and terraform destroy removes it. Thanks for watching.

# Rebuild from zero

Terraform owns the cluster, network, registry, identities and monitoring; GitHub Actions builds and pushes
images keylessly; ArgoCD pulls `k8s/base`; `deploy.sh` sequences the first bring-up. This page is the exact
order of operations plus the gotchas.

Time: ~25 minutes (Autopilot cluster ~8 min, ArgoCD ~3 min, SLO second apply after traffic).
Cost while up: ~₹150–200/day (Autopilot pods incl. ArgoCD + global external load balancer + NAT). Tear it down after the demo.

## 0. Prerequisites

| Need | Notes |
|---|---|
| Billing-linked GCP account | Budget + kill switch recommended. |
| `gcloud`, `terraform` >= 1.6, `kubectl`, `helm` 3, `gke-gcloud-auth-plugin` | `gcloud components install gke-gcloud-auth-plugin kubectl`; export `USE_GKE_GCLOUD_AUTH_PLUGIN=True`. |
| Go 1.27 (only for local tests) | `cd service && go test ./...` |
| A GitHub repo at `github_repo` (tfvars) | CI auth is bound to exactly this `owner/repo` by the WIF attribute condition. |
| Your public IP | `curl -s https://api.ipify.org` → `authorized_cidr`. Behind CGNAT the address rotates; use the ISP's /24 (as in the shipped tfvars). |

Organisation constraints handled by the repo (nothing to do, but know they exist):
`iam.automaticIamGrantsForDefaultServiceAccounts` → `terraform/iam.tf` grants the node SA `artifactregistry.reader`
explicitly (otherwise ImagePullBackOff 403). Domain-restricted sharing: WIF `principalSet` members are allowed; gmail
users are not, so there are no user bindings in Terraform.

## 1. Project

```bash
export PROJECT_ID=bj-gke-platform
gcloud projects create "$PROJECT_ID" --name "GKE platform"
gcloud billing projects link "$PROJECT_ID" --billing-account <BILLING_ACCOUNT_ID>
gcloud config set project "$PROJECT_ID"
gcloud auth application-default set-quota-project "$PROJECT_ID"
```

## 2. Configure

```bash
git clone https://github.com/bhavishyajain545/gcp-gke-platform && cd gcp-gke-platform
cp terraform/terraform.tfvars.example terraform/terraform.tfvars
# project_id, region, authorized_cidr (your IP/32 or /24), github_repo (owner/repo), alert_email
```

## 3. Bring-up

```bash
./deploy.sh            # 1 terraform apply  2 Cloud Build first image (sa-build)  3 kubeconfig
                       # 4 ArgoCD via helm + Application (k8s/argocd)  5 wait for rollout + gateway IP
# before the repo exists on GitHub: ./deploy.sh --direct   (kubectl apply -k k8s/base, no ArgoCD)
```

Expected first-apply errors: the two SLOs ("Cannot find metric") because the Prometheus series do not exist yet.
Generate traffic (step 4), wait ~10 minutes, then `terraform -chdir=terraform apply` again: SLOs, burn-rate alert and dashboard are created.

## 4. Verify

```bash
IP=$(kubectl -n url-health get gateway external -o jsonpath='{.status.addresses[0].value}')
curl -s -X POST http://$IP/checks -d '{"url":"https://www.google.com"}'
curl -s http://$IP/checks | jq
kubectl -n url-health get pods,networkpolicy,podmonitoring
kubectl -n argocd port-forward svc/argocd-server 8081:80 &    # UI at http://localhost:8081 (admin / secret below)
kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d
```

## 5. CI (keyless) and the GitOps loop

```bash
terraform -chdir=terraform output -raw workload_identity_provider   # projects/<num>/locations/global/workloadIdentityPools/github/providers/github-oidc
gh secret set GCP_WORKLOAD_IDENTITY_PROVIDER --repo <owner/repo> --body "<that value>"
```

Push any change under `service/` to `main`: the `ci` workflow runs `go test`, builds `sha-<short>` with Docker, pushes to
Artifact Registry as `sa-github-ci` (OIDC → WIF, no key), commits `newTag` into `k8s/base/kustomization.yaml` with `[skip ci]`,
and ArgoCD syncs it within 3 minutes. Check: `curl http://$IP/version`.

## 6. Re-record the demo (optional)

Needs: DemoProfile Chrome with `--remote-debugging-port=9222` signed in to the console and GitHub; the ArgoCD port-forward
on 8081 (it dies on idle, run it in a loop); `GH_TOKEN` (fine-grained PAT, contents RW) in the environment because the
GitOps scene makes a real commit and push; `DEMO_EXTRA_PATH` with gcloud and ffmpeg. Then `python -u demo\scenes.py`.
The recorder waits for the CI run and the cluster image tag, so a take is ~10 minutes.

## 7. Tear down (stop all cost)

Delete the Kubernetes objects first so the Gateway controller removes the load balancer it created (the LB is not in
Terraform state); then destroy.

```bash
./teardown.sh            # 1 kubectl delete Application + k8s/base (Gateway → LB cleanup)  2 terraform destroy
                         # 3 delete Gateway-controller leftovers by hand (forwarding rules, proxies, URL maps,
                         #   backend services, health checks, NEGs, addresses, GKE firewall rules)
                         # 4 terraform destroy again for the VPC  5 report of what is left
./audit-billing.sh       # every section should be empty
```

`teardown.sh` also works when the control plane is unreachable (your IP dropped out of `authorized_cidr`, which happened
during the real teardown): step 1 is skipped after a timeout and step 3 removes the load balancer resources, which are
**not** in Terraform state because the Gateway controller created them.

The WIF pool is soft-deleted for 30 days (no cost). Keeping the empty project costs nothing.

## Gotchas met while building

- ImagePullBackOff 403 → default compute SA needs `artifactregistry.reader` (org policy disables automatic grants).
- Readiness 503 with default-deny NetworkPolicy → DNS egress must allow any destination (NodeLocal DNSCache).
- "no healthy upstream" from the Gateway → `HealthCheckPolicy` pointing at `/healthz` (default probe is `/`).
- No metrics in Cloud Monitoring → NetworkPolicy must allow ingress from namespace `gke-gmp-system` (Managed Prometheus collector).
- SLO apply fails until the metric exists (see step 3). Service account IDs must be >= 6 chars (`sa-ci` was rejected → `sa-github-ci`).
- Control plane unreachable after the ISP changed your IP → update `authorized_cidr`, `terraform apply` the cluster.

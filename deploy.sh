#!/usr/bin/env bash
# End-to-end bring-up: Terraform -> first image -> ArgoCD -> Application.
#   ./deploy.sh            # full bring-up (GitOps via ArgoCD + GitHub repo)
#   ./deploy.sh --direct   # no ArgoCD: kubectl apply -k k8s/base (useful before the repo is on GitHub)
set -euo pipefail
cd "$(dirname "$0")"

PROJECT_ID="$(grep -E '^project_id' terraform/terraform.tfvars | cut -d'"' -f2)"
REGION="$(grep -E '^region' terraform/terraform.tfvars | cut -d'"' -f2)"
TAG="${TAG:-v1}"
MODE="${1:-gitops}"

echo "== 1/5 terraform apply"
terraform -chdir=terraform init -input=false >/dev/null
terraform -chdir=terraform apply -input=false -auto-approve
CLUSTER="$(terraform -chdir=terraform output -raw cluster_name)"

echo "== 2/5 build + push url-health-api:${TAG} (Cloud Build, sa-build)"
gcloud builds submit --project "$PROJECT_ID" --config cloudbuild.yaml --substitutions="_TAG=${TAG},_REGION=${REGION}" .

echo "== 3/5 kubeconfig"
gcloud container clusters get-credentials "$CLUSTER" --region "$REGION" --project "$PROJECT_ID"

if [[ "$MODE" == "--direct" ]]; then
  echo "== 4/5 kubectl apply -k k8s/base (direct mode)"
  kubectl apply -k k8s/base
else
  echo "== 4/5 ArgoCD (helm) + Application"
  helm repo add argo https://argoproj.github.io/argo-helm >/dev/null 2>&1 || true
  helm repo update >/dev/null
  helm upgrade --install argocd argo/argo-cd --namespace argocd --create-namespace \
    --set configs.params."server\.insecure"=true \
    --set server.resources.requests.cpu=250m --set server.resources.requests.memory=256Mi \
    --set controller.resources.requests.cpu=250m --set controller.resources.requests.memory=512Mi \
    --set repoServer.resources.requests.cpu=250m --set repoServer.resources.requests.memory=256Mi \
    --set redis.resources.requests.cpu=100m --set redis.resources.requests.memory=128Mi \
    --set applicationSet.enabled=false --set notifications.enabled=false --set dex.enabled=false \
    --wait --timeout 15m
  kubectl apply -f k8s/argocd/application.yaml
fi

echo "== 5/5 wait for rollout + gateway address"
kubectl -n url-health rollout status deployment/url-health-api --timeout=10m
for i in $(seq 1 60); do
  IP="$(kubectl -n url-health get gateway external -o jsonpath='{.status.addresses[0].value}' 2>/dev/null || true)"
  [[ -n "$IP" ]] && break; sleep 10
done
echo
echo "API: http://${IP:-<pending>}/checks"
echo "ArgoCD UI: kubectl -n argocd port-forward svc/argocd-server 8081:80  (admin password: kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)"

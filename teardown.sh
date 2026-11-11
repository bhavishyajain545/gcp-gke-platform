#!/usr/bin/env bash
# Full teardown of the platform so nothing bills afterwards.
#   ./teardown.sh            # 1 delete k8s objects (Gateway -> LB cleanup)  2 terraform destroy
#                            # 3 delete Gateway-controller leftovers the controller could not (or did not) remove
#                            # 4 terraform destroy again for the VPC if step 2 failed on dependencies  5 report
# Safe to re-run. Works even when the control plane is unreachable (e.g. your IP left authorized_cidr):
# step 1 is skipped after a timeout and step 3 removes the load balancer by hand.
set -uo pipefail
cd "$(dirname "$0")"
PROJECT_ID="$(grep -E '^project_id' terraform/terraform.tfvars | cut -d'"' -f2)"
REGION="$(grep -E '^region' terraform/terraform.tfvars | cut -d'"' -f2)"
export USE_GKE_GCLOUD_AUTH_PLUGIN=True
g() { gcloud "$@" --project "$PROJECT_ID" --quiet; }

echo "== 1/5 delete Kubernetes objects (so the Gateway controller removes its load balancer)"
if timeout 20 kubectl get ns url-health >/dev/null 2>&1; then
  timeout 120 kubectl delete -f k8s/argocd/application.yaml --ignore-not-found || true
  timeout 400 kubectl delete -k k8s/base --ignore-not-found --wait=true || true
  for i in $(seq 1 12); do
    n=$(g compute forwarding-rules list --format='value(name)' | wc -l); echo "  forwarding rules left: $n"
    [ "$n" = "0" ] && break; sleep 15
  done
else
  echo "  control plane unreachable or namespace gone; skipping (leftovers are removed in step 3)"
fi

echo "== 2/5 terraform destroy"
terraform -chdir=terraform init -input=false >/dev/null 2>&1 || true
terraform -chdir=terraform destroy -input=false -auto-approve 2>&1 | grep -v 'Still destroying' | tail -15

echo "== 3/5 Gateway / GKE leftovers (not in Terraform state), in dependency order"
del_all() {  # del_all <list args...> -- <delete args...>
  local list=(); while [ "$1" != "--" ]; do list+=("$1"); shift; done; shift
  for n in $(g "${list[@]}" --format='value(name)' 2>/dev/null); do echo "  delete $n"; g "$@" "$n" || true; done
}
del_all compute forwarding-rules list --global -- compute forwarding-rules delete --global
del_all compute forwarding-rules list --regions="$REGION" -- compute forwarding-rules delete --region="$REGION"
del_all compute target-https-proxies list --global -- compute target-https-proxies delete --global
del_all compute target-http-proxies list --global -- compute target-http-proxies delete --global
del_all compute url-maps list --global -- compute url-maps delete --global
del_all compute backend-services list --global -- compute backend-services delete --global
del_all compute backend-services list --regions="$REGION" -- compute backend-services delete --region="$REGION"
del_all compute health-checks list --global -- compute health-checks delete --global
del_all compute health-checks list --regions="$REGION" -- compute health-checks delete --region="$REGION"
for line in $(g compute network-endpoint-groups list --format='csv[no-heading](name,zone.basename(),region.basename())' 2>/dev/null); do
  IFS=, read -r n z r <<< "$line"; echo "  delete NEG $n"
  if [ -n "$z" ]; then g compute network-endpoint-groups delete "$n" --zone "$z" || true
  elif [ -n "$r" ]; then g compute network-endpoint-groups delete "$n" --region "$r" || true
  else g compute network-endpoint-groups delete "$n" --global || true; fi
done
del_all compute addresses list --global -- compute addresses delete --global
del_all compute addresses list --regions="$REGION" -- compute addresses delete --region="$REGION"
for n in $(g compute firewall-rules list --format='value(name)' 2>/dev/null | grep -v '^default-'); do echo "  delete firewall $n"; g compute firewall-rules delete "$n" || true; done

echo "== 4/5 terraform destroy again (VPC/subnet may have been blocked by the leftovers)"
terraform -chdir=terraform destroy -input=false -auto-approve 2>&1 | grep -v 'Still destroying' | tail -5

echo "== 5/5 what is left in $PROJECT_ID (expect only the default network, if any)"
echo "clusters:";          g container clusters list --format='value(name,status)'
echo "forwarding rules:";  g compute forwarding-rules list --format='value(name)'
echo "addresses:";         g compute addresses list --format='value(name)'
echo "NEGs:";              g compute network-endpoint-groups list --format='value(name)'
echo "routers:";           g compute routers list --format='value(name)'
echo "networks:";          g compute networks list --format='value(name)'
echo "artifact repos:";    g artifacts repositories list --format='value(name)' 2>/dev/null
echo "Firestore:";         g firestore databases list --format='value(name)' 2>/dev/null
echo "done. The WIF pool is soft-deleted for 30 days (free). An empty project costs nothing."

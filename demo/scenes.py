"""Demo video for Project 2: GKE Autopilot platform (GitOps CI/CD, Workload Identity, SLOs).

Prerequisites:
  * cluster deployed (`./deploy.sh`), API reachable through the Gateway
  * kubeconfig for the cluster in WSL (the gcloud/kubectl/terraform calls run there on Windows)
  * ArgoCD UI port-forwarded: kubectl -n argocd port-forward svc/argocd-server 8081:80
  * Chrome started with --remote-debugging-port=9222 and signed in to the Cloud console
  * GH_TOKEN in the environment (the GitOps scene pushes a real commit)

  python demo/scenes.py
"""

from __future__ import annotations

import asyncio
import json
import os
import re
import subprocess
import time
from pathlib import Path

from recorder import Demo

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent
PROJECT = "bj-gke-platform"
REGION = "us-central1"
REPO = "bhavishyajain545/gcp-gke-platform"
CDP_URL = os.environ.get("DEMO_CDP_URL", "http://127.0.0.1:9222")
ARGOCD_UI = "http://localhost:8081"

for p in os.environ.get("DEMO_EXTRA_PATH", "").split(os.pathsep):
    if p:
        os.environ["PATH"] = p + os.pathsep + os.environ["PATH"]
os.chdir(ROOT)


def sh(cmd: str) -> str:
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True)
    return (r.stdout + r.stderr).strip()


def wsl(cmd: str) -> str:
    """Run a command inside WSL where gcloud/kubectl/terraform/helm are set up (Windows host)."""
    if os.name != "nt":
        return sh(cmd)
    root = "/mnt/" + ROOT.drive[0].lower() + ROOT.as_posix()[2:]
    full = f"source ~/.gcp-env.sh; export USE_GKE_GCLOUD_AUTH_PLUGIN=True; cd {root}; {cmd}"
    r = subprocess.run(["wsl", "-e", "bash", "-c", full], capture_output=True, text=True)
    return (r.stdout + r.stderr).strip()


def cdp_available() -> bool:
    import urllib.request
    try:
        with urllib.request.urlopen(CDP_URL + "/json/version", timeout=2):
            return True
    except Exception:
        return False


NARRATION = {
    "arch": (
        "This is a small production-style platform on GKE Autopilot. A Go microservice called url-health-api registers URLs, probes them every minute "
        "and exposes Prometheus metrics. Every cloud resource is Terraform: the VPC with Cloud NAT, the private Autopilot cluster, Artifact Registry, "
        "Workload Identity, Firestore, and the monitoring SLOs. Delivery is GitOps: GitHub Actions builds and pushes the image with keyless auth, "
        "and ArgoCD inside the cluster pulls the manifests and keeps the cluster in sync."
    ),
    "api": (
        "First the running service, through the global external load balancer that the Gateway API created. I register two URLs, one healthy and one that returns 404, "
        "and a bad one that fails validation. A minute later the checker has probed both: Google's site is up with its latency, the broken one is down with the 404. "
        "The data lives in Firestore, written by the pod through Workload Identity."
    ),
    "identity": (
        "Here is the identity chain. The Kubernetes service account is annotated with the Google service account, the binding grants workload identity user "
        "on that account only, and the Google account itself has a single role: datastore user. There are no keys on it. "
        "The CI identity is similar: a workload identity pool trusts GitHub's OIDC issuer, restricted to this one repository, and maps to a service account "
        "that can only write to Artifact Registry. Nothing in this pipeline stores a credential."
    ),
    "gitops_push": (
        "Now the GitOps loop, live. I change the service, in this case the version endpoint, and push to main. "
        "GitHub Actions runs the tests, builds the image, pushes it to Artifact Registry using the federated token, and then commits the new image tag "
        "into the kustomization file. That commit is the deployment."
    ),
    "actions": (
        "This is the workflow run: the test job, then build and push. The auth step shows the workload identity exchange, no JSON key anywhere. "
        "The last step bumps the tag in k8s slash base and pushes it back to the repository."
    ),
    "argocd": (
        "ArgoCD watches that path. Once the tag commit lands, the application shows out of sync for a moment, then the automated sync rolls the deployment: "
        "a new ReplicaSet with the new image, surge one, zero unavailable, and the old pods drain after the readiness probe passes. "
        "Self-heal and prune are on, so manual drift is reverted and deleted manifests are removed."
    ),
    "rollout": (
        "Back on the command line, the deployment now runs the image tagged with the new commit, and the version endpoint returns it through the load balancer. "
        "From git push to a live rollout, no one touched kubectl."
    ),
    "slo": (
        "Observability is Google Managed Prometheus scraping the pod's metrics. On top of that, Cloud Monitoring has a custom service with two SLOs: "
        "ninety-nine percent of checks requests under 300 milliseconds, and ninety-nine point five percent non-5xx. "
        "The alert policy is a multi-window burn-rate alert, fourteen-point-four x over one hour or six x over six hours, which pages on sustained budget burn rather than single blips."
    ),
    "security": (
        "The security checklist, verified: private nodes with Cloud NAT, the control plane reachable only from one authorized IP, pods non-root with a read-only root filesystem and all capabilities dropped, "
        "a default-deny network policy with explicit allows for the load balancer, the Prometheus collector, DNS and outbound HTTPS, and a distroless image."
    ),
    "wrap": (
        "Everything shown is in the repository: the Go service with tests, Terraform for the platform, kustomize manifests, the ArgoCD application and the CI workflow. "
        "Autopilot bills per pod, so the whole platform costs a few rupees an hour while it runs, and terraform destroy removes it. Thanks for watching."
    ),
}


async def argocd_login_early(tab) -> None:
    """Sign in to the ArgoCD UI in the background tab before recording so the sync scene is instant."""
    pw = wsl("kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d")
    try:
        await tab.fill("input[name=username]", "admin", timeout=20000)
        await tab.fill("input[name=password]", pw)
        await tab.click("button[type=submit]")
        await tab.wait_for_url(re.compile(r"/applications"), timeout=30000)
        await tab.goto(f"{ARGOCD_UI}/applications/argocd/url-health-api?view=tree", wait_until="domcontentloaded")
    except Exception as exc:
        print(f"  ! argocd early login: {type(exc).__name__}")


async def main() -> None:
    use_cdp = cdp_available()
    demo = Demo(out_dir=HERE, cdp_url=CDP_URL if use_cdp else None)
    await demo.prepare(*NARRATION.values())

    # ---- real data for the terminal scenes (collected before recording) ----
    t0 = time.time()
    ip = wsl("kubectl -n url-health get gateway external -o jsonpath='{.status.addresses[0].value}'")
    api = f"http://{ip}"
    pods = wsl("kubectl -n url-health get pods -o wide | cut -c1-110")
    ksa = wsl("kubectl -n url-health get sa url-health-api -o jsonpath='{.metadata.annotations.iam\\.gke\\.io/gcp-service-account}'")
    gsa_roles = wsl(f"gcloud projects get-iam-policy {PROJECT} --flatten='bindings[].members' --filter='bindings.members:url-health-api@' --format='value(bindings.role)'")
    wi_binding = wsl(f"gcloud iam service-accounts get-iam-policy url-health-api@{PROJECT}.iam.gserviceaccount.com --format='value(bindings.members)'")
    keys = wsl(f"gcloud iam service-accounts keys list --iam-account url-health-api@{PROJECT}.iam.gserviceaccount.com --managed-by=user 2>&1")
    wif = wsl("terraform -chdir=terraform output -raw workload_identity_provider")
    wif_cond = wsl(f"gcloud iam workload-identity-pools providers describe github-oidc --location=global --workload-identity-pool=github --project={PROJECT} --format='value(attributeCondition)'")
    ci_roles = wsl(f"gcloud projects get-iam-policy {PROJECT} --flatten='bindings[].members' --filter='bindings.members:sa-github-ci@' --format='value(bindings.role)'")
    cluster = wsl(f"gcloud container clusters describe platform --region {REGION} --project {PROJECT} --format='value(privateClusterConfig.enablePrivateNodes,masterAuthorizedNetworksConfig.cidrBlocks[0].cidrBlock,workloadIdentityConfig.workloadPool)'")
    sec_ctx = wsl("kubectl -n url-health get deploy url-health-api -o jsonpath='{.spec.template.spec.securityContext}{\"\\n\"}{.spec.template.spec.containers[0].securityContext}'")
    netpol = wsl("kubectl -n url-health get networkpolicy")
    slo_list = wsl(f"gcloud monitoring slos list --service=url-health-api --project={PROJECT} --format='value(displayName,goal)' 2>&1 | head -3")
    print(f"pre-computed in {time.time() - t0:.0f}s; api={api}")

    page = await demo.start()
    gh_tab = argo_tab = slo_tab = None
    if use_cdp:
        gh_tab = await demo.new_tab(f"https://github.com/{REPO}/actions")
        argo_tab = await demo.new_tab(f"{ARGOCD_UI}/login")
        slo_tab = await demo.new_tab(f"https://console.cloud.google.com/monitoring/services/url-health-api?project={PROJECT}")
        await demo.capture(page)
        await argocd_login_early(argo_tab)

    # ---- 1 architecture ----
    async def s1():
        await page.goto((ROOT / "docs" / "architecture.html").as_uri())
        await page.evaluate("() => { document.body.style.zoom = '1.33'; document.body.style.margin = '0'; }")
    await demo.scene("Architecture", NARRATION["arch"], s1)

    # ---- 2 API through the gateway ----
    async def s2():
        post1 = sh(f'curl -s -X POST {api}/checks -H "content-type: application/json" -d "{{\\"url\\":\\"https://www.google.com\\"}}"')
        post2 = sh(f'curl -s -X POST {api}/checks -H "content-type: application/json" -d "{{\\"url\\":\\"https://example.com/missing\\"}}"')
        post3 = sh(f'curl -s -X POST {api}/checks -H "content-type: application/json" -d "{{\\"url\\":\\"ftp://nope\\"}}"')
        await demo.terminal("url-health-api via the Gateway (global external ALB)", [
            ("kubectl -n url-health get gateway external -o jsonpath='{.status.addresses[0].value}'", ip),
            ("kubectl -n url-health get pods -o wide", pods),
            (f"curl -s -X POST {api}/checks -d '{{\"url\":\"https://www.google.com\"}}'", post1),
            (f"curl -s -X POST {api}/checks -d '{{\"url\":\"https://example.com/missing\"}}'", post2),
            (f"curl -s -X POST {api}/checks -d '{{\"url\":\"ftp://nope\"}}'", post3),
        ], type_delay=6, pause=0.4)
        await asyncio.sleep(2)
        listing = sh(f"curl -s {api}/checks")
        try:
            d = json.loads(listing)
            listing = "\n".join(f"{c['status']:8s} {c['http_status']:>3}  {c['latency_ms']:>5} ms  {c['url']}" for c in d["checks"])
        except Exception:
            pass
        await page.evaluate("() => window.__prompt('')")
        for ch in f"curl -s {api}/checks | jq -r '.checks[] | [.status,.http_status,.latency_ms,.url] | @tsv'":
            await page.evaluate("(c) => window.__type(c)", ch)
            await asyncio.sleep(0.006)
        await page.evaluate("(o) => window.__output(o)", listing)
    await demo.scene("API", NARRATION["api"], s2, hold=1.5)

    # ---- 3 identity chain ----
    async def s3():
        await demo.terminal("Keyless identity: Workload Identity + Workload Identity Federation", [
            ("kubectl -n url-health get sa url-health-api -o jsonpath='{.metadata.annotations}'", f'{{"iam.gke.io/gcp-service-account":"{ksa}"}}'),
            (f"gcloud iam service-accounts get-iam-policy {ksa} --format='value(bindings.members)'", wi_binding),
            (f"gcloud projects get-iam-policy {PROJECT} --filter='bindings.members:url-health-api@' --format='value(bindings.role)'", gsa_roles),
            (f"gcloud iam service-accounts keys list --iam-account {ksa} --managed-by=user", keys or "Listed 0 items."),
            ("terraform output -raw workload_identity_provider", wif),
            ("gcloud iam workload-identity-pools providers describe github-oidc --format='value(attributeCondition)'", wif_cond),
            (f"gcloud projects get-iam-policy {PROJECT} --filter='bindings.members:sa-github-ci@' --format='value(bindings.role)'", ci_roles),
        ], type_delay=6, pause=0.4)
    await demo.scene("Identity", NARRATION["identity"], s3, hold=1.0)

    # ---- 4 GitOps: real code change + push ----
    push_sha = {"sha": ""}

    async def s4():
        main_go = ROOT / "service" / "main.go"
        src = main_go.read_text(encoding="utf-8")
        stamp = time.strftime("%Y-%m-%d %H:%M UTC", time.gmtime())
        marker = "// demo:"
        new_line = f"{marker} version endpoint touched on {stamp}"
        src = re.sub(r"// demo:.*\n", "", src)
        src = src.replace("// Version is set at build time", f"{new_line}\n// Version is set at build time", 1)
        main_go.write_text(src, encoding="utf-8")
        tok = os.environ.get("GH_TOKEN", "")
        sh("git add service/main.go")
        sh(f'git -c user.name="Bhavishya Jain" -c user.email="jainbhavishya545@gmail.com" commit -q -m "service: touch version endpoint for the GitOps demo"')
        out = sh(f"git push -q https://x-access-token:{tok}@github.com/{REPO}.git main")
        push_sha["sha"] = sh("git rev-parse --short HEAD")
        await demo.terminal("GitOps: push a change", [
            ("git diff --stat HEAD~1", sh("git diff --stat HEAD~1")),
            ("git log --oneline -1", sh("git log --oneline -1")),
            ("git push origin main", out.replace(tok, "***") or f"To github.com:{REPO}.git\n   main -> main"),
        ], type_delay=10, pause=0.5)
    await demo.scene("GitOps push", NARRATION["gitops_push"], s4, hold=1.0)

    # ---- 5 GitHub Actions run ----
    if use_cdp:
        async def s5():
            await gh_tab.goto(f"https://github.com/{REPO}/actions", wait_until="domcontentloaded")
            await demo.capture(gh_tab)
            # open the newest run once it appears
            for _ in range(20):
                try:
                    link = gh_tab.locator("a[href*='/actions/runs/']").first
                    await link.click(timeout=3000)
                    break
                except Exception:
                    await asyncio.sleep(3)
                    await gh_tab.reload(wait_until="domcontentloaded")
            await asyncio.sleep(3)
            # wait (real time) for the run to finish so the rollout scenes have something to show
            for _ in range(60):
                txt = await gh_tab.evaluate("() => document.body.innerText")
                if "build-push" in txt and ("succeeded" in txt.lower() or "success" in txt.lower()) and "in progress" not in txt.lower():
                    break
                await asyncio.sleep(10)
                try:
                    await gh_tab.reload(wait_until="domcontentloaded")
                except Exception:
                    pass
            await asyncio.sleep(2)
        await demo.scene("GitHub Actions", NARRATION["actions"], s5, hold=1.5)

        # ---- 6 ArgoCD sync ----
        async def argocd_login():
            pw = wsl("kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d")
            await argo_tab.goto(f"{ARGOCD_UI}/login?return_url={ARGOCD_UI}/applications/argocd/url-health-api?view=tree", wait_until="domcontentloaded")
            try:
                await argo_tab.fill("input[name=username]", "admin", timeout=15000)
                await argo_tab.fill("input[name=password]", pw)
                await argo_tab.click("button[type=submit]")
                await argo_tab.wait_for_selector("text=url-health-api", timeout=30000)
            except Exception as exc:
                print(f"  ! argocd login: {type(exc).__name__}")

        async def s6():
            if "/login" in argo_tab.url or "applications" not in argo_tab.url:
                await argocd_login()
            await argo_tab.goto(f"{ARGOCD_UI}/applications/argocd/url-health-api?view=tree", wait_until="domcontentloaded")
            await demo.capture(argo_tab)
            # wait for ArgoCD to pick up the tag commit (poll up to ~4 min) by watching the deployed image tag in-cluster
            for _ in range(30):
                img = wsl("kubectl -n url-health get deploy url-health-api -o jsonpath='{.spec.template.spec.containers[0].image}'")
                if push_sha["sha"] and push_sha["sha"] in img:
                    break
                await asyncio.sleep(10)
            await asyncio.sleep(3)
        await demo.scene("ArgoCD", NARRATION["argocd"], s6, hold=1.5)
        await demo.capture(page)

    # ---- 7 rollout proof ----
    async def s7():
        wsl("kubectl -n url-health rollout status deployment/url-health-api --timeout=4m >/dev/null 2>&1")
        img = wsl("kubectl -n url-health get deploy url-health-api -o jsonpath='{.spec.template.spec.containers[0].image}'")
        hist = wsl("kubectl -n url-health rollout history deployment/url-health-api | tail -4")
        ver = sh(f"curl -s {api}/version")
        tag = wsl("grep newTag k8s/base/kustomization.yaml")
        await demo.terminal("Rollout: from git push to a live pod", [
            ("git pull -q && grep newTag k8s/base/kustomization.yaml", tag),
            ("kubectl -n url-health get deploy url-health-api -o jsonpath='{.spec.template.spec.containers[0].image}'", img),
            ("kubectl -n url-health rollout history deployment/url-health-api", hist),
            ("kubectl -n url-health get pods", wsl("kubectl -n url-health get pods")),
            (f"curl -s {api}/version", ver),
        ], type_delay=6, pause=0.5)
    await demo.scene("Rollout", NARRATION["rollout"], s7, hold=1.0)

    # ---- 8 SLOs ----
    if use_cdp:
        async def s8():
            await slo_tab.goto(f"https://console.cloud.google.com/monitoring/services/url-health-api?project={PROJECT}", wait_until="commit", timeout=120000)
            await demo.capture(slo_tab)
            try:
                await slo_tab.wait_for_selector("text=/checks", timeout=90000)
            except Exception:
                pass
            await asyncio.sleep(4)
        await demo.scene("SLOs", NARRATION["slo"], s8, hold=1.5)
        await demo.capture(page)
    else:
        async def s8b():
            await demo.terminal("SLOs (Cloud Monitoring)", [("gcloud monitoring slos list --service=url-health-api", slo_list)])
        await demo.scene("SLOs", NARRATION["slo"], s8b)

    # ---- 9 security ----
    async def s9():
        await demo.terminal("Security checklist, verified", [
            (f"gcloud container clusters describe platform --region {REGION} --format='value(privateClusterConfig.enablePrivateNodes,masterAuthorizedNetworksConfig.cidrBlocks[0].cidrBlock,workloadIdentityConfig.workloadPool)'", cluster),
            ("kubectl -n url-health get deploy url-health-api -o jsonpath='{.spec.template.spec.securityContext}{.spec.template.spec.containers[0].securityContext}'", sec_ctx),
            ("kubectl -n url-health get networkpolicy", netpol),
            ("grep -A1 '^FROM gcr.io/distroless' service/Dockerfile", "FROM gcr.io/distroless/static-debian12:nonroot\nCOPY --from=build /out/url-health-api /url-health-api"),
        ], type_delay=6, pause=0.5)
    await demo.scene("Security", NARRATION["security"], s9, hold=1.0)

    # ---- 10 wrap ----
    async def s10():
        await demo.terminal("Infrastructure as code + cleanup", [
            ("terraform -chdir=terraform state list | wc -l", str(len(wsl("terraform -chdir=terraform state list").splitlines()))),
            ("terraform -chdir=terraform output", wsl("terraform -chdir=terraform output -no-color")),
            ("# when done: terraform -chdir=terraform destroy", "Autopilot bills per pod; destroy removes the cluster, LB, NAT, registry and SLOs."),
        ], type_delay=8, pause=0.5)
    await demo.scene("Wrap-up", NARRATION["wrap"], s10, hold=1.5)

    await demo.finish("demo")


if __name__ == "__main__":
    asyncio.run(main())

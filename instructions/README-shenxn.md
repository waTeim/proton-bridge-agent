# Instructions for chart‑authoring agent (shenxn/protonmail-bridge)

**Goal:** produce a minimal, working Helm chart for Proton Mail Bridge using the `shenxn/protonmail-bridge` container. The agent may over‑analyze; these instructions are structured as **tight, testable checklists** so that extra exploration doesn’t derail the outcome.

---

## 0) Definition of “done” (must all be true)

- Chart installs successfully with `helm install`.
- Pod starts and stays running.
- A PVC is mounted at `/root` and survives restarts.
- Service exposes ports **1025 (SMTP)** and **1143 (IMAP)**.
- NOTES.txt clearly explains first‑time init via `kubectl exec`.

If any of the above is missing, the task is **not done**.

---

## 1) Container contract (do not invent)

**Image:** `shenxn/protonmail-bridge` (configurable tag)

**Initialization (one‑time, interactive):**
```
docker run --rm -it -v protonmail:/root shenxn/protonmail-bridge init
```
Inside the CLI: `login` → `info` → `exit`.

**Runtime (non‑interactive):**
```
docker run -d -v protonmail:/root -p 1025:25 -p 1143:143 shenxn/protonmail-bridge
```

**Ports:**
- Container **25** → Service **1025** (SMTP)
- Container **143** → Service **1143** (IMAP)

**Persistence:** `/root` must be persisted. Do **not** split into multiple mounts; a single PVC at `/root` is acceptable and simplest.

---

## 2) Chart structure (exact files to touch)

Only change these files in the `helm create` scaffold:

1. `values.yaml`
2. `templates/deployment.yaml` **or** `templates/statefulset.yaml`
3. `templates/service.yaml`
4. `templates/pvc.yaml` (or volumeClaimTemplates if using StatefulSet)
5. `templates/NOTES.txt`

**Avoid** adding extra templates, CRDs, sidecars, or controllers.

---

## 3) Values.yaml (minimum keys required)

Add or ensure these values exist:

```yaml
image:
  repository: shenxn/protonmail-bridge
  tag: latest    # allow override
  pullPolicy: IfNotPresent

service:
  type: ClusterIP
  smtpPort: 1025
  imapPort: 1143

persistence:
  enabled: true
  size: 1Gi
  accessMode: ReadWriteOnce
  # storageClass: ""
```

No other values are required for MVP.

---

## 4) Work steps (resistant to over‑thinking)

Perform **in order**, do not skip:

1) **Pick controller:**
   - Prefer **StatefulSet** (stable identity helps manual init), but Deployment is acceptable.
   - If you pick StatefulSet, use `volumeClaimTemplates` for `/root`.

2) **Mount PVC at `/root`:**
   - Single PVC, mounted to `/root`.
   - Do not mount subpaths unless necessary.

3) **Expose ports:**
   - Container ports: 25 + 143
   - Service ports: 1025 + 1143

4) **Set args (if any):**
   - Do **not** add flags unless documented by the image.
   - Default command is fine; no extra args needed.

5) **NOTES.txt:**
   - Must include exact init steps (kubectl exec, `init`, `login`, `info`, `exit`).

Stop after these are complete; do not add extra features.

---

## 5) NOTES.txt content (verbatim guidance)

Include this snippet (adjust release name):

```
First‑time setup (one‑time):
1) Get the pod name:
   kubectl get pods -l app.kubernetes.io/instance={{ .Release.Name }}
2) Exec into the pod and run init:
   kubectl exec -it <POD> -- sh -c "protonmail-bridge init"
3) In the CLI: login → info → exit

After init, the pod will run normally and expose IMAP/SMTP on the service ports.
```

---

## 6) Definition of scope creep (do NOT do these)

- No ingress, no TLS, no cert‑manager.
- No database or sidecars.
- No secret injection or GPG key handling (this image doesn’t require it).
- No extra services or metrics.

If tempted, stop and verify the “done” checklist instead.

---

## 7) Sanity checklist before finishing

- [ ] Chart installs
- [ ] Pod running
- [ ] PVC mounted at `/root`
- [ ] Service ports 1025/1143 present
- [ ] NOTES.txt includes init steps

If all boxes are checked, you are finished.

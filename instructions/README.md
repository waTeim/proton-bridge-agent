# Instructions for chart authoring agent (keep on-point)

You are a creative but sometimes overly deep-thinking agent. Stay concise and deliver a working Helm chart with minimal scope creep.

## Objective
Create a **simple Helm chart** (from a `helm create` scaffold) that runs Proton Mail Bridge in Kubernetes.

## Non-negotiable requirements
1. **Image**: `syphr/proton-bridge` (configurable in values.yaml).
2. **Run mode**: default **non-interactive** (`--non-interactive` or `--noninteractive`).
3. **Ports**: expose SMTP **1025** and IMAP **1143** via a Service.
4. **Persistence**: single PVC mounted to these paths:
   - `/root/.config/protonmail/bridge` (required)
   - `/root/.password-store` (required)
   - `/root/.cache/protonmail/bridge` (optional but included)
5. **Secret**: `BRIDGE_GPG_KEY` must come from a Kubernetes Secret (string data). Do **not** place it in ConfigMap.
6. **Values.yaml** must include:
   - image.repository, image.tag
   - service ports (1025/1143)
   - persistence.enabled + size
   - secret creation + BRIDGE_GPG_KEY
7. **NOTES.txt** must explain first-time setup:
   - `kubectl exec` into pod
   - run `proton-bridge --cli`, `login`, `info`, then `exit`

## Scope boundaries
- **No** extra services, operators, or sidecars.
- **No** ingress or external exposure by default (ClusterIP only).
- **No** clever templating beyond standard Helm patterns.
- **No** secrets in git beyond placeholders.

## Deliverables
- Updated templates for Deployment/StatefulSet, Service, PVC (or volumeClaimTemplates), Secret.
- Updated values.yaml.
- Updated NOTES.txt.

## Style
- Keep changes minimal and readable.
- If uncertain about flags, prefer `--non-interactive` and document it.
- Do not add unrelated features.

End state: a minimal, clean chart that boots the bridge, stores state, and is ready for manual first-time login.

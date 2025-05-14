# MinKAPI

minkapi is minimal, lean implmeentation of  Kubernetes API Server - an  in-memory implementation that can mimic core functions of an kubernetes API server and simulate initialization of some core resources such as Nodes, Pods. 

Primarily meant to offer enough of the surface area of the kubernetes API server so that the Kube Scheduler and the Kubernetes Cluster Autoscaler can operate their controller loops.

> [!NOTE]
> This is WIP presently.

## Usage 

### For End User

1. Install: `go install  https://github.com/elankath/minkapi`
2. Launch: `minkapi` # KUBECONFIG put in `/tmp/minkapi.yaml`
3. Connect via `kubectl`:  
   - Create Node: `kubectl create -v=8 --validate=false -f <nodeSpecYamlPath>`
   - Create Pod: `kubectl create -v=8 --validate=false -f <podSpecYamlPath>`
   - List Nodes: `kubectl get no`
   - List Pods: `kubectl get po`

### For Developer

1. Checkout: `https://github.com/elankath/minkapi`
2. Launch `make run`: # KUBECONFIG put in `/tmp/minkapi.yaml`
3. Connect via `kubectl`:
   - Create Node: `kubectl create -v=8 --validate=false -f specs/node-a.yaml`
   - Create Pod: `kubectl create -v=8 --validate=false -f specs/pod-a.yaml`
   - List Nodes: `kubectl get no`
   - List Pods: `kubectl get po`
4. See other make targets: `make help`







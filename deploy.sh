#! /bin/bash

if [ $# -ne 4 ]
then
    echo 'usage: ./deploy.sh $namespace $release $duration $interval'
    exit 1
fi

namespace=$1
release=$2
duration=$3
interval=$4

if [ $duration -le $interval ]
then
    echo "the duration value need larger than interval value"
    exit 1
fi
if [ $duration -lt 60 ]
then
    echo "the duration value cannot less than 60"
    exit 1
fi

kubectl apply -f - <<EOF
kind: Role
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: milvus-gather-service-reader
  namespace: ${namespace}
rules:
- apiGroups: [""] # "" indicates the core API group
  resources: ["services","pods","deployments"]
  verbs: ["get", "watch", "list"]
- apiGroups: ["apps"] # "" indicates the core API group
  resources: ["replicasets","deployments"]
  verbs: ["get", "watch", "list"]
- apiGroups: [""]
  resources: ["pods/exec"]
  verbs: ["create"]
---
EOF

if [ $? -ne 0 ]
then
    echo "You don't have permission to create Role, please check your k8s user permissions."
    exit 1
fi

kubectl apply -f - <<EOF
kind: RoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: milvus-gather-service-reader
  namespace: ${namespace}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: milvus-gather-service-reader
subjects:
- kind: ServiceAccount
  name: default
  namespace: ${namespace}
---
EOF

if [ $? -ne 0 ]
then
    echo "You don't have permission to create RoleBinding, please check your k8s user permissions."
    exit 1
fi

kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: milvus-gather-config
  namespace: ${namespace}
data:
  config.json: |
    {
        "namespace":"${namespace}",
        "release":"${release}",
        "duration": ${duration},
        "interval": ${interval}
    }
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: milvus-gather
  labels:
    app: milvus-gather
  namespace: ${namespace}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: milvus-gather
  template:
    metadata:
      labels:
        app: milvus-gather
    spec:
      containers:
      - name: milvus-gather
        image: kevin1991/milvus-gather:latest
        volumeMounts:
        - mountPath: /milvus/config
          name: milvus-gather-config
      volumes:
      - configMap:
          name: milvus-gather-config
        name: milvus-gather-config
EOF

echo "start gather info......"
echo "wait $((duration + interval)) seconds"
sleep $((duration + interval))
echo "finish gather info......"


echo "cp data.tar.gz from pod to local"
pod=`kubectl get pod -n ${namespace}|grep milvus-gather|awk '{print $1}'`
kubectl exec pod/${pod} -n ${namespace} -- tar zcf data.tar.gz data
kubectl cp ${pod}:data.tar.gz data.tar.gz -n ${namespace}



echo "delete resource......"
kubectl delete deployment milvus-gather -n ${namespace} 
kubectl delete configmap milvus-gather-config -n ${namespace}
kubectl delete rolebinding milvus-gather-service-reader -n ${namespace} 
kubectl delete role milvus-gather-service-reader -n ${namespace} 

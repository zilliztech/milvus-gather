## Purpose
gather milvus info for debug and troubleshooting, then upload info when create milvus issue.
- gather milvus base info: version and milvus deployment name
- gather milvus goroutine and profile
- gather milvus pod cpu and mem usage
- gather milvus important metrics

## Metrics collected
- container_cpu_usage_seconds_total
- container_memory_working_set_bytes
- milvus_proxy_sq_latency_bucket
- milvus_proxy_sq_latency_sum
- milvus_proxy_sq_latency_count
- milvus_proxy_mutation_latency_bucket
- milvus_proxy_mutation_latency_sum
- milvus_proxy_mutation_latency_count
- milvus_rootcoord_time_tick_delay
- milvus_proxy_search_vectors_count
- milvus_proxy_insert_vectors_count
- milvus_proxy_req_count

## Quickstart
```bash
# usage: ./deploy.sh $namespace $release $duration $interval
# namespace: the milvus namespace
# release: the milvus release name
# duration: how long, unit is second
# interval: scrap metric interval,unit is second
$ bash deploy.sh default my-release 300 30
```

## Upload
The above script will generate data.tar.gz file locally. You can upload data.tar.gz file when you create milvus issue. 


## Diagnose
When user uploaded the data.tar.gz file, we can use it to diagnose issue.
The included files are as follows
- base-info: saved milvus base info,version and milvus deployment name
- goroutine-.*: saved milvus components goroutine info
- profile-.*: saved milvus components profile info, can use ```go tool pprof $profile``` diagnose the issues
- metrics-info: saved milvus metrics info

## Use prometheus view metrics info
```bash
$ mkdir /tmp/data
$ docker run -p 9090:9090 -v /tmp/data:/prometheus prom/prometheus
# https://prometheus.io/docs/prometheus/latest/storage/#backfilling-from-openmetrics-format
$ promtool tsdb create-blocks-from openmetrics metrics-info /tmp/data
# view metric graph on the browser,url is 127.0.0.1:9090
```
![image](https://github.com/zilliztech/milvus-gather/blob/master/metric.png)

FROM busybox:latest
RUN mkdir -p /milvus/data
WORKDIR /milvus
COPY milvus-gather /milvus
ENTRYPOINT ["./milvus-gather"]

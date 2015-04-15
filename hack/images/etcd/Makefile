.PHONY:	clean build push

IMAGE = etcd
TAG = 2.0.9
OUTPUT_DIR = $(IMAGE)-v$(TAG)-linux-amd64
REPO = jdef/k8sm-$(IMAGE)

clean:
	rm -rf $(OUTPUT_DIR) $(IMAGE)-v$(TAG)-linux-amd64.tar.gz etcd etcdctl

build: clean
	curl -L -O https://github.com/coreos/etcd/releases/download/v$(TAG)/$(IMAGE)-v$(TAG)-linux-amd64.tar.gz
	tar xzvf $(IMAGE)-v$(TAG)-linux-amd64.tar.gz
	cp $(OUTPUT_DIR)/etcd .
	cp $(OUTPUT_DIR)/etcdctl .
	cp /etc/ssl/certs/ca-certificates.crt .
	sudo docker build -t $(REPO):$(TAG) .

push:	build
	sudo docker push $(REPO):$(TAG)

all:	push

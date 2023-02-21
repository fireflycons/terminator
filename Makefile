TAG?=0.0.4
REGISTRY=fireflycons
REPO?=${REGISTRY}
IMAGE=terminator

all: push deploy

.PHONY: pwd
pwd:
	@echo ${PWD}

.PHONY: build
build:
	docker build -t ${REPO}/${IMAGE}:${TAG} .

.PHONY: push
push: build
	docker push ${REPO}/${IMAGE}:${TAG}

.PHONY: deploy
deploy:
	kubectl config set-context --current --namespace monitoring
	@helm upgrade --install terminator ./charts/terminator --set-json 'args=["--grace-period=15m"]' --set image.pullPolicy=Always

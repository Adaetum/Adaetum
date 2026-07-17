FROM mirror.gcr.io/library/python:3.12-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      openssh-client \
      curl \
      ca-certificates \
      cron \
    && curl -fsSL https://tailscale.com/install.sh | sh \
    && pip install --no-cache-dir ansible-core \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /ansible/ansible

COPY ansible/ansible-scripts/ /usr/local/bin/
RUN chmod +x /usr/local/bin/run-ansible /usr/local/bin/cron-entrypoint /usr/local/bin/build-inventory

ENV CRON_SCHEDULE="*/10 * * * *"
ENV ANSIBLE_PLAYBOOK="playbooks/day2.yml"
ENV ANSIBLE_INVENTORY="node-inventory.yml"
ENV ANSIBLE_EXTRA_ARGS=""

ENTRYPOINT ["/usr/local/bin/cron-entrypoint"]

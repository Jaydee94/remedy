FROM debian:bookworm-slim
# iproute2 is for `ss`, procps for ps, free and uptime.
RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-server sudo fish procps iproute2 \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd
# remedy logs in with bash; fishy has a shell that is not POSIX, to prove that the canary refuses it. '*' keeps the password
# locked and the key login possible.
RUN useradd -m -s /bin/bash -p '*' remedy && useradd -m -s /usr/bin/fish -p '*' fishy
COPY fakes/docker /usr/bin/docker
COPY fakes/systemctl /usr/bin/systemctl
COPY fakes/journalctl /usr/bin/journalctl
COPY sudoers-remedy /etc/sudoers.d/remedy
COPY sshd_config.d/remedy.conf /etc/ssh/sshd_config.d/remedy.conf
RUN chmod 0755 /usr/bin/docker /usr/bin/systemctl /usr/bin/journalctl && chmod 0440 /etc/sudoers.d/remedy && visudo -cf /etc/sudoers.d/remedy
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]

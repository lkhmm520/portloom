#!/bin/sh
set -eu
umask 077
port=${PORTLOOM_SSH_PORT:-2222}
case "$port" in *[!0-9]*|'') echo 'PORTLOOM_SSH_PORT must be an integer' >&2; exit 1;; esac
if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then echo 'PORTLOOM_SSH_PORT is outside 1..65535' >&2; exit 1; fi
install -d -m 0755 /run/sshd /hostkeys
if [ ! -s /hostkeys/ssh_host_ed25519_key ]; then
  rm -f /hostkeys/ssh_host_ed25519_key /hostkeys/ssh_host_ed25519_key.pub
  ssh-keygen -q -t ed25519 -N '' -C portloom-managed-sshd -f /hostkeys/ssh_host_ed25519_key
elif [ ! -s /hostkeys/ssh_host_ed25519_key.pub ]; then
  ssh-keygen -y -f /hostkeys/ssh_host_ed25519_key > /hostkeys/ssh_host_ed25519_key.pub.next
  mv /hostkeys/ssh_host_ed25519_key.pub.next /hostkeys/ssh_host_ed25519_key.pub
fi
chmod 0600 /hostkeys/ssh_host_ed25519_key
chmod 0644 /hostkeys/ssh_host_ed25519_key.pub
/usr/sbin/sshd -t -f /etc/ssh/sshd_config.portloom -o "Port=$port" -h /hostkeys/ssh_host_ed25519_key
touch /run/portloom-sshd.ready
exec /usr/sbin/sshd -D -e -f /etc/ssh/sshd_config.portloom -o "Port=$port" -h /hostkeys/ssh_host_ed25519_key

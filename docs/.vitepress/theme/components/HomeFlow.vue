<script setup lang="ts">
import { computed } from 'vue'
import { useData } from 'vitepress'
const { lang } = useData()
const en = computed(() => lang.value.startsWith('en'))
</script>
<template>
  <section class="flow-section" aria-labelledby="flow-title">
    <div class="flow-copy">
      <span class="eyebrow">DATA PLANE</span>
      <h2 id="flow-title">{{ en ? 'Keep your TLS and DNS. Replace fragile tunnel scripts.' : '保留已有 TLS 与 DNS，替换难维护的隧道脚本' }}</h2>
      <p>{{ en ? 'PortLoom separates desired state, SSH forwarding, and public ingress into verifiable layers. NPM keeps terminating HTTPS while Agents create restricted loopback reverse forwards.' : 'PortLoom 把期望状态、SSH 转发与公网入口拆成可验证的层。NPM 继续终止 HTTPS，Agent 只建立受限的 loopback 反向转发。' }}</p>
    </div>
    <div class="flow-diagram" role="img" aria-label="PortLoom traffic flow">
      <div class="flow-node"><small>PUBLIC</small><strong>Browser</strong><span>HTTPS</span></div><i>→</i>
      <div class="flow-node"><small>VPS</small><strong>NPM</strong><span>TLS + Host</span></div><i>→</i>
      <div class="flow-node accent"><small>PORTLOOM</small><strong>Gateway</strong><span>:8081</span></div><i>→</i>
      <div class="flow-node"><small>SSH</small><strong>Reverse tunnel</strong><span>loopback only</span></div><i>→</i>
      <div class="flow-node"><small>NAS</small><strong>Local service</strong><span>127.0.0.1</span></div>
    </div>
  </section>
</template>

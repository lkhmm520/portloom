<script setup lang="ts">
import { computed } from 'vue'
import { useData } from 'vitepress'
const { lang } = useData()
const en = computed(() => lang.value.startsWith('en'))
</script>
<template>
  <section class="host-flow" aria-labelledby="host-flow-title">
    <div class="host-flow-copy">
      <span>{{ en ? 'TWO DOCKER HOSTS' : '两台 DOCKER 主机' }}</span>
      <h2 id="host-flow-title">{{ en ? 'Server faces the internet. Agent stays inside.' : 'Server 在公网，Agent 留在内网' }}</h2>
      <p>{{ en ? 'Install each side once. After that, add services and hostnames in the WebUI.' : '两边各安装一次。之后新增服务和域名，都在 WebUI 完成。' }}</p>
    </div>
    <div class="host-map" role="img" :aria-label="en ? 'PortLoom two-host traffic diagram' : 'PortLoom 两台主机访问示意图'">
      <div class="map-node visitor"><small>{{ en ? 'PUBLIC USER' : '公网访问者' }}</small><strong>{{ en ? 'Browser' : '浏览器' }}</strong><em>HTTPS</em></div>
      <div class="map-arrow request"><b>→</b><span>{{ en ? 'request' : '访问请求' }}</span></div>
      <div class="map-node server"><small>{{ en ? 'PUBLIC VPS' : '公网 VPS' }}</small><strong>PortLoom Server</strong><em>WebUI · HTTPS · SSH</em></div>
      <div class="map-arrow tunnel"><b>⇄</b><span>{{ en ? 'encrypted tunnel' : '加密隧道' }}</span></div>
      <div class="map-node agent"><small>{{ en ? 'NAS / PRIVATE HOST' : 'NAS / 内网主机' }}</small><strong>PortLoom Agent</strong><em>{{ en ? 'local web service' : '本地 Web 服务' }}</em></div>
    </div>
    <div class="connect-direction"><i></i><strong>{{ en ? 'Connection direction:' : '建连方向：' }}</strong> {{ en ? 'Agent actively connects to Server. No inbound NAS port is opened.' : 'Agent 主动连接 Server，NAS 不开放入站端口。' }}</div>
    <div class="install-steps">
      <div><b>1</b><span><strong>{{ en ? 'Install Server' : '安装 Server' }}</strong><small>{{ en ? 'Start compose.yml or use the optional installer' : '启动 compose.yml，或选择自动安装器' }}</small></span></div>
      <div><b>2</b><span><strong>{{ en ? 'Open WebUI' : '打开 WebUI' }}</strong><small>{{ en ? 'Generate the Agent command' : '生成 Agent 安装命令' }}</small></span></div>
      <div><b>3</b><span><strong>{{ en ? 'Paste on NAS' : '粘贴到 NAS' }}</strong><small>{{ en ? 'The Agent enrolls and connects' : 'Agent 自动注册并连接' }}</small></span></div>
    </div>
  </section>
</template>

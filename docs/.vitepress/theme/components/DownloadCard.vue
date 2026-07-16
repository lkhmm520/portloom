<script setup lang="ts">
import { withBase } from 'vitepress'
const props = defineProps<{ title: string; description: string; file: string }>()
</script>
<template><a class="download-card" :href="withBase(`/examples/${props.file}`)" download><span><strong>{{ props.title }}</strong><small>{{ props.description }}</small></span><b aria-hidden="true">↓</b></a></template>

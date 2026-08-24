<template>
  <AppLayout>
    <div class="-m-4 md:-m-6 lg:-m-8">
      <ImageCanvasHost route-mode="user" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import ImageCanvasHost from '@/components/image-canvas/ImageCanvasHost.vue'
</script>

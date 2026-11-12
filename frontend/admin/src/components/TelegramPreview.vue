<script setup lang="ts">
defineProps<{
  title: string;
  body: string;
  fields?: Record<string, unknown>;
}>();
</script>
<template>
  <section class="telegram-preview" aria-label="پیش‌نمایش پیام">
    <p class="eyebrow">پیش‌نمایش پیام</p>
    <article>
      <strong>{{ title || "عنوان پیام" }}</strong>
      <p>{{ body }}</p>
      <p v-for="(value, label) in fields" :key="label">
        <strong>{{ label }}:</strong> {{ value }}
      </p>
      <small>همراه دانشگاه · پیش‌نمایش</small>
    </article>
  </section>
</template>

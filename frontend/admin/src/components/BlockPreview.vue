<script setup lang="ts">
import type { RichDocument } from "../lib/studio";
import { safeRichHTML } from "../lib/studio";
defineProps<{ document: RichDocument; title: string }>();
</script>
<template>
  <section
    class="telegram-preview studio-preview"
    aria-label="پیش‌نمایش پیام پیشرفته"
  >
    <div class="preview-label">پیش‌نمایش · بدون ارسال</div>
    <article class="telegram-bubble">
      <h3>{{ title || "عنوان پیام" }}</h3>
      <template v-for="(b, i) in document.blocks" :key="i"
        ><section v-if="b.enabled" class="preview-block">
          <h4 v-if="b.title">{{ b.title }}</h4>
          <p v-if="b.description">{{ b.description }}</p>
          <div
            v-if="['text', 'heading', 'typing'].includes(b.type)"
            :class="{ 'preview-heading': b.type === 'heading' }"
            v-html="safeRichHTML(b.text)"
          ></div>
          <details v-if="b.type === 'details'" :open="b.open">
            <summary>بیشتر بخوانید</summary>
            <div v-html="safeRichHTML(b.text)"></div>
          </details>
          <div v-if="b.type === 'table'" class="table-scroll">
            <table>
              <tbody>
                <tr v-for="(row, r) in b.rows" :key="r">
                  <component
                    :is="b.header && r === 0 ? 'th' : 'td'"
                    v-for="(cell, c) in row"
                    :key="c"
                    >{{ cell }}</component
                  >
                </tr>
              </tbody>
            </table>
          </div>
          <img
            v-if="b.type === 'image' && b.file_id"
            :src="'/api/v1/admin/files/media?id=' + b.file_id"
            alt="تصویر پیام"
            loading="lazy"
          />
          <div v-if="b.type === 'slideshow'" class="slide-preview">
            <figure v-for="(s, j) in b.slides" :key="j">
              <img
                :src="'/api/v1/admin/files/media?id=' + s.file_id"
                :alt="s.caption || 'اسلاید'"
                loading="lazy"
              />
              <figcaption>{{ s.caption }}</figcaption>
            </figure>
          </div>
          <audio
            v-if="b.type === 'audio' && b.file_id"
            controls
            :src="'/api/v1/admin/files/media?id=' + b.file_id"
          ></audio>
          <pre v-if="b.type === 'formula'">{{ b.latex }}</pre>
          <p v-if="b.type === 'formula'">{{ b.example }}</p>
          <pre v-if="b.type === 'chart'">{{
            JSON.stringify(b.chart, null, 2)
          }}</pre>
          <div
            v-if="b.type === 'buttons'"
            class="preview-buttons"
            :style="{
              gridTemplateColumns: `repeat(${b.columns},minmax(0,1fr))`,
            }"
          >
            <button
              v-for="(item, j) in b.items"
              :key="j"
              type="button"
              :class="item.style"
              :disabled="item.disabled"
              @click.prevent
            >
              {{ item.action === "toggle" ? "☐ " : "" }}{{ item.icon
              }}{{ item.text }}<small v-if="item.action === 'url'">↗</small>
            </button>
          </div>
        </section></template
      >
    </article>
  </section>
</template>

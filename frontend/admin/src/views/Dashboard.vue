<script setup lang="ts">
import { computed, onMounted } from "vue";
import { useRoute } from "vue-router";
import { useSystem } from "../stores/system";
import { modules } from "../lib/modules";
const system = useSystem(),
  route = useRoute();
const current = computed(
  () => modules.find((m) => m.id === route.query.module) || modules[0],
);
onMounted(() => system.refresh());
</script>
<template>
  <div class="page-heading">
    <div>
      <p class="eyebrow">زیرساخت تازه، همان همراه دانشگاه</p>
      <h1>
        {{ current.id === "overview" ? "آماده برای قدم بعدی" : current.name }}
      </h1>
      <p class="muted">{{ current.text }}</p>
    </div>
    <span class="pill">پیش‌نمایش توسعه</span>
  </div>
  <section class="notice" aria-label="وضعیت انتقال">
    <span class="notice-icon">↗</span>
    <div>
      <strong>نسخهٔ جدید در حال ساخت است</strong>
      <p>
        ابزارهای منتقل‌شده از پنل مدیریت در دسترس‌اند. اطلاعات نصب عملیاتی هنوز
        منتقل نشده و اتصال ربات واقعی فعال نیست.
      </p>
    </div>
  </section>
  <div v-if="current.id === 'overview'" class="metric-grid">
    <article class="metric">
      <span>سرویس Go</span
      ><strong>{{
        system.failed
          ? "در دسترس نیست"
          : system.status
            ? "متصل"
            : "در حال بررسی"
      }}</strong
      ><small>وضعیت واقعی API</small>
    </article>
    <article class="metric">
      <span>دیتابیس PostgreSQL</span
      ><strong>{{
        system.status?.databaseReady
          ? "آماده"
          : system.status
            ? "نیازمند بررسی"
            : "—"
      }}</strong
      ><small>اتصال و migration پایه</small>
    </article>
    <article class="metric">
      <span>انتقال داده‌ها</span><strong>انجام نشده</strong
      ><small>دادهٔ عملیاتی در این پنل نیست</small>
    </article>
    <article class="metric">
      <span>اتصال ربات</span><strong>غیرفعال</strong
      ><small>رفتار نسخهٔ اصلی تغییر نکرده</small>
    </article>
  </div>
  <div class="content-grid">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <h2>
            {{
              current.id === "overview" ? "مسیر توسعهٔ ماژول‌ها" : current.name
            }}
          </h2>
          <p class="muted">
            مرز هر بخش مشخص است؛ ابزارها مرحله‌به‌مرحله منتقل می‌شوند.
          </p>
        </div>
        <span class="small-badge">اسکلت اولیه</span>
      </div>
      <div v-if="current.id === 'overview'" class="module-grid">
        <RouterLink
          v-for="m in modules.slice(1)"
          :key="m.id"
          :to="{ path: '/', query: { module: m.id } }"
          class="module-card"
          ><span class="module-icon">{{ m.icon }}</span>
          <div>
            <strong>{{ m.name }}</strong
            ><small>در انتظار انتقال</small>
          </div>
          <span class="arrow">←</span></RouterLink
        >
      </div>
      <div v-else class="empty-state">
        <span class="empty-icon">{{ current.icon }}</span>
        <h3>جای این ماژول آماده است</h3>
        <p>
          فرم‌ها، داده‌ها و مجوزهای این بخش پس از انتقال و آزمون فعال می‌شوند.
        </p>
        <RouterLink class="button secondary" to="/"
          >بازگشت به نمای کلی</RouterLink
        >
      </div>
    </section>
    <section class="panel progress-panel">
      <p class="eyebrow">قدم‌های روشن</p>
      <h2>وضعیت این مرحله</h2>
      <ol class="milestones">
        <li>
          <span class="done">✓</span>
          <div>
            <strong>ممیزی و نقشهٔ انتقال</strong
            ><small>ساختار فعلی و قراردادهای حساس ثبت شده‌اند</small>
          </div>
        </li>
        <li>
          <span class="done">✓</span>
          <div>
            <strong>زیرساخت اجرایی</strong
            ><small>API، دیتابیس و پوستهٔ پنل</small>
          </div>
        </li>
        <li>
          <span class="current">۳</span>
          <div>
            <strong>هویت و مجوزها</strong
            ><small>نشست امن و مجوزهای مدیران آماده است</small>
          </div>
        </li>
      </ol>
      <button
        class="button"
        :disabled="system.loading"
        @click="system.refresh()"
      >
        {{ system.loading ? "در حال بررسی…" : "بررسی دوباره اتصال" }}
      </button>
      <p v-if="system.failed" class="error" role="alert">
        اتصال به سرویس برقرار نشد. سرویس محلی را بررسی کنید.
      </p>
    </section>
  </div>
</template>

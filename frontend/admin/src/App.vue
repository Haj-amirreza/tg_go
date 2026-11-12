<script setup lang="ts">
import { computed, ref } from "vue";
import { useRoute } from "vue-router";
import { useAuth } from "./stores/auth";
import { useSystem } from "./stores/system";
import { modules } from "./lib/modules";
const auth = useAuth();
const system = useSystem();
const route = useRoute(),
  menu = ref(false),
  search = ref("");
const dark = ref(localStorage.getItem("university-theme-v3") === "dark");
function theme() {
  document.documentElement.classList.toggle("dark", dark.value);
  localStorage.setItem("university-theme-v3", dark.value ? "dark" : "light");
}
theme();
const selected = computed(() =>
  route.path === "/manage/support"
    ? "support"
    : route.path === "/manage/student-life"
      ? "personal"
      : route.path === "/manage/leisure"
        ? "leisure"
        : route.path === "/manage/studio"
          ? "studio"
          : route.path === "/manage/catalog"
            ? route.query.kind === "books"
              ? "library"
              : "people"
            : String(route.query.module || "overview"),
);
function destination(id: string) {
  if (auth.actor && id === "support" && auth.can("tickets"))
    return { path: "/manage/support" };
  if (
    auth.actor &&
    id === "personal" &&
    (auth.can("market") || auth.can("content") || auth.can("users"))
  )
    return { path: "/manage/student-life" };
  if (auth.actor && id === "leisure" && auth.can("content"))
    return { path: "/manage/leisure" };
  if (
    auth.actor &&
    auth.can("content") &&
    (id === "studio" || id === "announcements")
  )
    return { path: "/manage/studio" };
  if (
    auth.actor &&
    auth.can("content") &&
    (id === "people" || id === "library")
  )
    return {
      path: "/manage/catalog",
      query: { kind: id === "library" ? "books" : "professors" },
    };
  return { path: "/", query: id === "overview" ? {} : { module: id } };
}
const filtered = computed(() =>
  modules.filter((m) => m.name.includes(search.value.trim())),
);
</script>
<template>
  <div class="workspace">
    <button
      v-if="menu"
      class="backdrop"
      aria-label="بستن فهرست"
      @click="menu = false"
    ></button>
    <aside class="sidebar" :class="{ open: menu }" aria-label="بخش‌های پنل">
      <RouterLink to="/" class="brand" @click="menu = false"
        ><span class="brand-icon">هـ</span
        ><span>همراه دانشگاه<small>فضای مدیریت جدید</small></span></RouterLink
      >
      <p class="nav-caption">دانشگاه، در یک نگاه</p>
      <label class="search-label"
        ><span class="sr-only">جست‌وجوی بخش‌ها</span
        ><input v-model="search" placeholder="جست‌وجوی بخش‌ها…" type="search"
      /></label>
      <nav>
        <RouterLink
          v-for="m in filtered"
          :key="m.id"
          :to="destination(m.id)"
          :class="{
            active:
              selected === m.id &&
              (route.path === '/' || route.path.startsWith('/manage/')),
          }"
          @click="menu = false"
          ><span aria-hidden="true">{{ m.icon }}</span
          >{{ m.name }}</RouterLink
        >
      </nav>
      <div class="sidebar-note">
        <span class="status-dot"></span
        >{{
          system.status?.environment === "production"
            ? "محیط عملیاتی"
            : system.status
              ? "محیط توسعه"
              : "در حال بررسی اتصال"
        }}
        <small>{{
          system.status
            ? system.status.botConnected
              ? "ربات در این نصب فعال است"
              : "اتصال ربات در این نصب غیرفعال است"
            : "وضعیت سرویس هنوز دریافت نشده"
        }}</small>
      </div>
    </aside>
    <div class="main-shell">
      <header class="topbar">
        <div class="topbar-start">
          <button
            class="icon-button mobile-menu"
            :aria-expanded="menu"
            aria-label="باز کردن فهرست"
            @click="menu = !menu"
          >
            ☰</button
          ><span>پنل مدیریت <span class="muted">/ نسخهٔ جدید</span></span>
        </div>
        <div class="topbar-actions">
          <button
            class="icon-button"
            aria-label="تغییر حالت روشن و تاریک"
            @click="
              dark = !dark;
              theme();
            "
          >
            {{ dark ? "☀" : "☾" }}</button
          ><RouterLink v-if="auth.actor" class="button secondary" to="/manage"
            >حساب مدیریت</RouterLink
          >
        </div>
      </header>
      <main id="main"><RouterView /></main>
      <footer>
        Go · PostgreSQL · Vue <span>پنل یکپارچهٔ همراه دانشگاه</span>
      </footer>
    </div>
  </div>
</template>

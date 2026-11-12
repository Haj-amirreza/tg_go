<script setup lang="ts">
import type { components } from "../lib/api.generated";
import { ref, onMounted } from "vue";
import { useRouter } from "vue-router";
import { useAuth, type Role } from "../stores/auth";
const auth = useAuth(),
  router = useRouter(),
  loading = ref(true),
  error = ref(""),
  roles = ref<Role[]>([]);
const performance = ref<components["schemas"]["Performance"] | null>(null);
async function refreshPerformance() {
  try {
    performance.value = await auth.performance();
  } catch {
    error.value = "دریافت وضعیت عملکرد انجام نشد.";
  }
}
async function clearCache() {
  try {
    await auth.clearCache();
    await refreshPerformance();
  } catch {
    error.value = "پاک‌سازی کش انجام نشد.";
  }
}
const jobs = ref<components["schemas"]["QueueItem"][]>([]),
  queueBusy = ref(false);
async function refreshQueue(after = "0") {
  try {
    jobs.value = await auth.queueJobs(after);
  } catch {
    error.value = "دریافت صف انجام نشد.";
  }
}
async function queueAction(path: string, value: unknown) {
  if (queueBusy.value) return;
  queueBusy.value = true;
  try {
    await auth.queueAction(path, value);
    await refreshQueue();
  } catch {
    error.value = "عملیات صف انجام نشد؛ وضعیت دوباره بررسی شود.";
  } finally {
    queueBusy.value = false;
  }
}
const labels: Record<string, string> = {
  users: "کاربران",
  content: "ویرایش محتوا",
  publish: "تأیید و انتشار",
  taxonomy: "ساختار آموزشی",
  files: "فایل‌ها",
  campaigns: "ارسال و کمپین",
  tickets: "پشتیبانی",
  market: "بازارچه",
  settings: "تنظیمات",
  bot: "ربات و تلگرام",
  queue: "صف و زمان‌بندی",
  logs: "رخدادها",
  health: "سلامت سرویس",
  backups: "پشتیبان",
  reports: "گزارش‌ها",
  admins: "مدیران و نقش‌ها",
};
onMounted(async () => {
  try {
    await auth.load();
    if (!auth.actor) {
      await router.replace("/login");
      return;
    }
    if (auth.can("admins")) roles.value = await auth.roles();
    if (auth.can("health")) await refreshPerformance();
    if (auth.can("queue")) await refreshQueue();
  } catch {
    error.value = "دریافت اطلاعات حساب انجام نشد. دوباره وارد شوید.";
  } finally {
    loading.value = false;
  }
});
async function leave() {
  try {
    await auth.logout();
    await router.replace("/login");
  } catch {
    error.value = "خروج انجام نشد؛ دوباره تلاش کنید.";
  }
}
</script>
<template>
  <p v-if="loading" class="muted" role="status">در حال بررسی نشست…</p>
  <p v-if="error" role="alert" class="error">{{ error }}</p>
  <template v-if="auth.actor && !loading">
    <RouterLink v-if="auth.can('content')" class="button" to="/manage/catalog"
      >مدیریت اساتید و کتابخانه</RouterLink
    >
    <div class="page-heading">
      <div>
        <p class="eyebrow">پنل اختصاصی مدیران</p>
        <h1>سلام، {{ auth.actor.name }}</h1>
        <p class="muted">
          {{ auth.actor.role }} · سطح دسترسی {{ auth.actor.level }}
        </p>
      </div>
      <button class="button secondary" @click="leave">خروج امن</button>
    </div>
    <section
      v-if="auth.can('health')"
      class="panel roles-panel"
      aria-label="پایش عملکرد"
    >
      <h2>عملکرد سرویس</h2>
      <p class="muted">
        وضعیت همین فرایند؛ آمار صف با حداکثر یک ثانیه تأخیر. هیچ ظرفیت کاربری از
        این اعداد نتیجه‌گیری نمی‌شود.
      </p>
      <div v-if="performance" class="permission-grid">
        <div>
          اتصال دیتابیس: {{ performance.pool.total }} /
          {{ performance.pool.max }}
        </div>
        <div>queryهای ثبت‌شده: {{ performance.database.queries }}</div>
        <div>
          کش: {{ performance.cache.hits }} hit /
          {{ performance.cache.misses }} miss
        </div>
        <div>
          حافظهٔ heap: {{ (performance.heapBytes / 1048576).toFixed(1) }} MiB
        </div>
        <div v-for="(count, state) in performance.queue" :key="state">
          {{ state }}: {{ count }}
        </div>
      </div>
      <button class="button secondary" @click="refreshPerformance">
        تازه‌سازی وضعیت
      </button>
      <button class="button secondary" @click="clearCache">
        پاک‌سازی کش عمومی
      </button>
    </section>
    <section
      v-if="auth.can('queue')"
      class="panel roles-panel"
      aria-label="مدیریت صف"
    >
      <h2>مدیریت صف</h2>
      <p class="muted">
        نمایش ۲۰تایی بدون متن پیام یا مشخصات مخاطب. ارسال با نتیجهٔ نامعلوم
        خودکار تکرار نمی‌شود.
      </p>
      <button
        class="button secondary"
        :disabled="queueBusy"
        @click="queueAction('control', { paused: true })"
      >
        توقف پردازش صف
      </button>
      <button
        class="button secondary"
        :disabled="queueBusy"
        @click="queueAction('control', { paused: false })"
      >
        ادامه پردازش صف
      </button>
      <button class="button secondary" @click="refreshQueue()">
        تازه‌سازی صف
      </button>
      <ul class="role-list">
        <li v-for="job in jobs" :key="job.id">
          <span
            >#{{ job.id }} · {{ job.kind }} · {{ job.state }} ·
            {{ job.attempts }} تلاش</span
          ><button
            v-if="job.state === 'dead'"
            class="button secondary"
            :disabled="queueBusy"
            @click="queueAction('retry', { id: job.id })"
          >
            تلاش دوباره
          </button>
        </li>
      </ul>
      <p v-if="!jobs.length" class="muted">موردی در این صفحه نیست.</p>
      <button
        v-if="jobs.length === 20"
        class="button secondary"
        @click="refreshQueue(jobs[jobs.length - 1]!.id)"
      >
        صفحهٔ بعد
      </button>
    </section>
    <section class="notice">
      <p>
        ورود و بررسی دسترسی فعال است. ابزارهای محتوایی و داده‌های نسخهٔ قبلی در
        مرحله‌های بعد منتقل می‌شوند.
      </p>
    </section>
    <section class="panel">
      <h2>دسترسی‌های حساب شما</h2>
      <p class="muted">این مجوزها در هر درخواست از سرور بررسی می‌شوند.</p>
      <ul class="permission-grid">
        <li v-for="p in auth.actor.permissions" :key="p">
          <span aria-hidden="true">✓</span>{{ labels[p] || p }}
        </li>
      </ul>
      <p v-if="!auth.actor.permissions.length" class="muted">
        هنوز مجوز بخشی برای این حساب ثبت نشده است.
      </p>
    </section>
    <section v-if="auth.can('admins')" class="panel roles-panel">
      <h2>نقش‌های قابل مشاهده</h2>
      <p class="muted">
        مدیر محدود تنها نقش‌های پایین‌تر از سطح خود را می‌بیند. ابزار ویرایش
        نقش‌ها در مرحلهٔ مدیریت افزوده می‌شود.
      </p>
      <ul class="role-list">
        <li v-for="role in roles" :key="role.id">
          <strong>{{ role.name }}</strong
          ><span class="muted"
            >سطح {{ role.level }} · {{ role.permissions.length }} مجوز</span
          >
        </li>
      </ul>
    </section>
  </template>
</template>

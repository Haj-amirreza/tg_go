<script setup lang="ts">
import { ref, onMounted } from "vue";
import { useRouter } from "vue-router";
import { useAuth, AuthError } from "../stores/auth";
import { useSystem } from "../stores/system";
const auth = useAuth(),
  system = useSystem(),
  router = useRouter();
const username = ref(""),
  password = ref(""),
  otp = ref(""),
  busy = ref(false),
  error = ref("");
const params = new URLSearchParams(window.location.search);
const grant = params.get("tg_admin") || "";
const initData =
  new URLSearchParams(window.location.hash.slice(1)).get("tgWebAppData") || "";
const mini = Boolean(grant);
// Remove the capability and signed launch data from browser history before any request.
if (grant || initData) window.history.replaceState(null, "", "/login");
onMounted(async () => {
  await system.refresh();
  try {
    await auth.load();
    if (auth.actor) await router.replace("/manage");
  } catch {
    error.value = "سرویس ورود در دسترس نیست.";
  }
});
function updateOTP(event: Event) {
  const input = event.target as HTMLInputElement;
  const value = input.value.replace(/[۰-۹٠-٩]/g, (digit) =>
    String(digit.charCodeAt(0) - (digit >= "۰" ? 1776 : 1632)),
  );
  otp.value = value;
  input.value = value;
}
async function submit() {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    if (mini) {
      await auth.signIn({ grant, initData, otp: otp.value }, true);
    } else {
      await auth.signIn({
        username: username.value,
        password: password.value,
        otp: otp.value,
      });
    }
    password.value = "";
    await router.replace("/manage");
  } catch (e) {
    error.value =
      e instanceof AuthError
        ? e.message
        : "ارتباط برقرار نشد؛ دوباره تلاش کنید.";
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <section class="panel login-panel">
    <p class="eyebrow">فضای اختصاصی مدیران</p>
    <h1>ورود امن مدیر</h1>
    <p class="muted">برای ادامه از حساب مدیریتی ثبت‌شدهٔ خود استفاده کنید.</p>
    <div v-if="!system.status?.authenticationReady" class="notice">
      <p>سرویس ورود هنوز آماده نیست. تنظیمات محیط و اتصال را بررسی کنید.</p>
    </div>
    <form class="auth-form" :aria-busy="busy" @submit.prevent="submit">
      <template v-if="!mini">
        <label for="username">نام کاربری</label
        ><input
          id="username"
          v-model="username"
          autocomplete="username"
          dir="ltr"
          maxlength="80"
          required
        />
        <label for="password">رمز عبور</label
        ><input
          id="password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          dir="ltr"
          required
        />
      </template>
      <div v-else class="notice">
        <p>
          {{
            initData
              ? "ورود از تلگرام؛ کد دومرحله‌ای را در صورت فعال بودن وارد کنید."
              : "این لینک را از دکمهٔ مدیریت در تلگرام باز کنید."
          }}
        </p>
      </div>
      <label for="otp"
        >کد دومرحله‌ای <small class="muted">اگر فعال کرده‌اید</small></label
      ><input
        id="otp"
        :value="otp"
        @input="updateOTP"
        inputmode="numeric"
        autocomplete="one-time-code"
        pattern="[0-9]{6}"
        maxlength="6"
        dir="ltr"
        placeholder="۱۲۳۴۵۶"
      />
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <button
        class="button"
        :disabled="
          busy || !system.status?.authenticationReady || (mini && !initData)
        "
      >
        {{ busy ? "در حال بررسی…" : mini ? "ادامه با تلگرام" : "ورود به پنل" }}
      </button>
    </form>
    <RouterLink class="text-link" to="/">بازگشت به نمای توسعه ←</RouterLink>
  </section>
</template>

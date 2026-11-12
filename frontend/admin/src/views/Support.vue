<script setup lang="ts">
import { ref, onMounted, reactive } from "vue";
import { useAuth } from "../stores/auth";
import { domainRequest as api, commandKey } from "../lib/domain-api";
type Ticket = {
  id: string;
  userId: string;
  subject: string;
  category: string;
  state: string;
  priority: string;
  assignedTo: number | null;
  revision: number;
  updatedAt: number;
};
type Message = {
  id: string;
  adminId: number | null;
  body: string;
  internal: boolean;
  createdAt: number;
};
type Thread = {
  ticket: Ticket;
  messages: Message[];
  page: number;
  pages: number;
};
const auth = useAuth(),
  tickets = ref<Ticket[]>([]),
  thread = ref<Thread | null>(null),
  assignees = ref<{ id: string; name: string }[]>([]),
  page = ref(0),
  state = ref(""),
  error = ref(""),
  notice = ref(""),
  busy = ref(false),
  preview = ref(false),
  body = ref(""),
  internal = ref(false);
const draft = reactive({
  state: "answered",
  priority: "normal",
  assignedTo: "",
});
let signature = "",
  key = "";
function keyFor(v: unknown) {
  const value = JSON.stringify(v);
  if (signature !== value) {
    signature = value;
    key = commandKey();
  }
  return key;
}
async function run(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  try {
    await fn();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function load() {
  tickets.value = await api(`tickets?page=${page.value}&state=${state.value}`);
}
async function open(id: string, p?: number) {
  await run(async () => {
    thread.value = await api(
      `tickets/thread?id=${id}${p === undefined ? "" : "&page=" + p}`,
    );
    const t = thread.value!.ticket;
    Object.assign(draft, {
      state: t.state,
      priority: t.priority,
      assignedTo: t.assignedTo === null ? "" : String(t.assignedTo),
    });
    body.value = "";
    preview.value = false;
  });
}
async function save() {
  if (!thread.value) return;
  await run(async () => {
    const payload = {
      id: thread.value!.ticket.id,
      revision: thread.value!.ticket.revision,
      body: body.value,
      internal: internal.value,
      state: draft.state,
      priority: draft.priority,
      assignedTo: draft.assignedTo === "" ? null : Number(draft.assignedTo),
    };
    thread.value = await api(
      "tickets/reply",
      "POST",
      payload,
      auth.actor?.csrf,
      keyFor(payload),
    );
    notice.value = internal.value
      ? "یادداشت داخلی ذخیره شد؛ برای دانشجو ارسال نمی‌شود."
      : "تغییرات ذخیره شد و پاسخ در صف ارسال قرار گرفت.";
    body.value = "";
    preview.value = false;
    await load();
  });
}
onMounted(async () => {
  await auth.load();
  if (auth.can("tickets"))
    await run(async () => {
      await load();
      assignees.value = await api("tickets/assignees");
    });
});
</script>
<template>
  <div class="catalog-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">هر سؤال، یک پاسخ روشن</p>
        <h1>پرسش و پشتیبانی</h1>
        <p class="muted">
          گفتگوهای دانشجویان، پاسخ مدیر و یادداشت‌های داخلی در یک پرونده.
        </p>
      </div>
      <RouterLink class="button secondary" to="/manage">حساب مدیریت</RouterLink>
    </header>
    <p v-if="error" class="error-box" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice-box" role="status">{{ notice }}</p>
    <p v-if="!auth.can('tickets')" role="alert">
      اجازهٔ مدیریت این بخش را ندارید.
    </p>
    <template v-else
      ><div class="catalog-toolbar">
        <label
          >فیلتر گفتگوها<select
            v-model="state"
            aria-label="فیلتر گفتگوها"
            :disabled="busy"
            @change="
              page = 0;
              run(load);
            "
          >
            <option value="">همهٔ گفتگوها</option>
            <option value="open">باز</option>
            <option value="reviewing">در حال بررسی</option>
            <option value="answered">پاسخ داده شده</option>
            <option value="closed">بسته</option>
          </select></label
        ><button class="button secondary" :disabled="busy" @click="run(load)">
          تازه‌سازی
        </button>
      </div>
      <div class="support-layout">
        <section class="panel">
          <h2>پرونده‌ها</h2>
          <button
            v-for="t in tickets.slice(0, 10)"
            :key="t.id"
            class="thread-card"
            :class="{ selected: thread?.ticket.id === t.id }"
            :disabled="busy"
            @click="open(t.id)"
          >
            <strong>#{{ t.id }} · {{ t.subject }}</strong
            ><span>{{ t.category }} · {{ t.state }} · {{ t.priority }}</span>
          </button>
          <p v-if="!tickets.length">گفتگویی در این صفحه نیست.</p>
          <div class="catalog-pagination">
            <button
              class="button secondary"
              :disabled="busy || page === 0"
              @click="
                page--;
                run(load);
              "
            >
              صفحه قبل</button
            ><span>{{ page + 1 }}</span
            ><button
              class="button secondary"
              :disabled="busy || tickets.length < 11"
              @click="
                page++;
                run(load);
              "
            >
              صفحه بعد
            </button>
          </div>
        </section>
        <section v-if="thread" class="panel support-thread">
          <header class="section-heading">
            <h2>#{{ thread.ticket.id }} · {{ thread.ticket.subject }}</h2>
            <small>کاربر {{ thread.ticket.userId }}</small>
          </header>
          <div class="messages">
            <article
              v-for="m in thread.messages"
              :key="m.id"
              class="support-message"
              :class="{ staff: m.adminId, internal: m.internal }"
            >
              <strong>{{
                m.internal ? "یادداشت داخلی" : m.adminId ? "مدیر" : "دانشجو"
              }}</strong>
              <p>{{ m.body }}</p>
              <small>{{
                new Date(m.createdAt * 1000).toLocaleString("fa-IR")
              }}</small>
            </article>
          </div>
          <div class="catalog-pagination">
            <button
              class="button secondary"
              :disabled="busy || thread.page === 0"
              @click="open(thread.ticket.id, thread.page - 1)"
            >
              پیام‌های قبل</button
            ><span>{{ thread.page + 1 }} از {{ thread.pages }}</span
            ><button
              class="button secondary"
              :disabled="busy || thread.page + 1 >= thread.pages"
              @click="open(thread.ticket.id, thread.page + 1)"
            >
              پیام‌های بعد
            </button>
          </div>
          <form class="reply-editor" @submit.prevent="preview = true">
            <label
              >متن پاسخ<textarea
                v-model="body"
                rows="5"
                maxlength="5000"
              ></textarea></label
            ><label
              ><input v-model="internal" type="checkbox" /> یادداشت داخلی؛ برای
              دانشجو ارسال نشود</label
            >
            <div class="catalog-toolbar">
              <label
                >وضعیت پرونده<select
                  v-model="draft.state"
                  aria-label="وضعیت پرونده"
                >
                  <option value="open">باز</option>
                  <option value="reviewing">در حال بررسی</option>
                  <option value="answered">پاسخ داده شده</option>
                  <option value="closed">بسته</option>
                </select></label
              ><label
                >اولویت<select v-model="draft.priority" aria-label="اولویت">
                  <option value="low">کم</option>
                  <option value="normal">عادی</option>
                  <option value="high">بالا</option>
                </select></label
              ><label
                >مسئول رسیدگی<select
                  v-model="draft.assignedTo"
                  aria-label="مسئول رسیدگی"
                >
                  <option value="">بدون مسئول</option>
                  <option v-for="a in assignees" :key="a.id" :value="a.id">
                    {{ a.name }}
                  </option>
                </select></label
              >
            </div>
            <button class="button" :disabled="busy">
              پیش‌نمایش پاسخ و تغییرات
            </button>
          </form>
          <section
            v-if="preview"
            class="notice-box reply-preview"
            role="region"
            aria-label="پیش‌نمایش پاسخ"
          >
            <strong>{{
              internal ? "فقط برای مدیران" : "پاسخ قابل ارسال به دانشجو"
            }}</strong>
            <p>{{ body || "فقط وضعیت پرونده تغییر می‌کند." }}</p>
            <small>{{ draft.state }} · {{ draft.priority }}</small>
            <div class="catalog-toolbar">
              <button class="button" :disabled="busy" @click="save">
                تأیید و ذخیره پاسخ</button
              ><button
                class="button secondary"
                :disabled="busy"
                @click="preview = false"
              >
                بازگشت به ویرایش
              </button>
            </div>
          </section>
        </section>
        <section v-else class="panel empty-state">
          یک گفتگو را برای مشاهدهٔ پیام‌ها انتخاب کن.
        </section>
      </div></template
    >
  </div>
</template>

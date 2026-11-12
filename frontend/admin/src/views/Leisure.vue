<script setup lang="ts">
import { ref, reactive, onMounted, computed } from "vue";
import { useAuth } from "../stores/auth";
import { domainRequest as api, commandKey } from "../lib/domain-api";
const kinds: Record<string, string> = {
    poem: "شعر و فال",
    recommendation: "معرفی کتاب",
    word: "پانتومیم",
    quote: "جملهٔ روز",
    excerpt: "تکه‌کتاب",
    riddle: "چیستان",
  },
  fieldSets: Record<string, string[]> = {
    poem: ["poet", "name", "title", "source", "verses", "interpretation"],
    recommendation: ["category", "title", "author", "kind", "summary", "why"],
    word: ["level", "text"],
    quote: ["text"],
    excerpt: ["text", "source"],
    riddle: ["question", "answer"],
  },
  labels: Record<string, string> = {
    poet: "شاعر",
    name: "نام شاعر",
    title: "عنوان",
    source: "منبع",
    verses: "مصراع‌ها؛ هر مصراع در یک خط",
    interpretation: "تفسیر ادبی",
    category: "دستهٔ معرفی",
    author: "نویسنده",
    kind: "گونهٔ کتاب",
    summary: "دربارهٔ کتاب",
    why: "چرا بخوانم؟",
    level: "سطح",
    text: "متن",
    question: "چیستان",
    answer: "پاسخ",
  };
const selected = ref<string[]>([]);
const auth = useAuth(),
  kind = ref("poem"),
  records = ref<Record<string, Record<string, Record<string, unknown>>>>({}),
  revision = ref(0),
  page = ref(0),
  error = ref(""),
  notice = ref(""),
  busy = ref(false),
  editing = ref(false),
  id = ref(""),
  draft = reactive<Record<string, string>>({}),
  search = ref("");
const rows = computed(() =>
    Object.entries(records.value[kind.value] || {}).filter(([, r]) =>
      String(r.title || r.text || r.question || "").includes(
        search.value.trim(),
      ),
    ),
  ),
  fields = computed(() => fieldSets[kind.value] || []);
let signature = "",
  key = "";
function keyFor(v: unknown) {
  const s = JSON.stringify(v);
  if (signature !== s) {
    signature = s;
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
async function archiveSelection() {
  if (!selected.value.length || !confirm("موارد انتخاب‌شده حذف شوند؟")) return;
  await run(async () => {
    const payload = {
      kind: kind.value,
      ids: selected.value,
      revision: revision.value,
    };
    await api(
      "leisure/archive",
      "POST",
      payload,
      auth.actor?.csrf,
      keyFor(payload),
    );
    selected.value = [];
    notice.value = "محتواهای انتخاب‌شده حذف شدند.";
    await load();
  });
}
async function load() {
  const v = await api<{ records: typeof records.value; revision: number }>(
    "leisure",
  );
  records.value = v.records;
  revision.value = v.revision;
}
function edit(key: string, row: Record<string, unknown>) {
  for (const k in draft) delete draft[k];
  for (const field of fields.value)
    draft[field] =
      field === "verses"
        ? ((row[field] as string[]) || []).join("\n")
        : String(row[field] || "");
  id.value = key;
  editing.value = true;
  signature = "";
}
function fresh() {
  edit("custom_" + commandKey().slice(0, 24), {
    poet: "hafez",
    name: "حافظ",
    category: "short",
    level: "easy",
  });
}
async function save(remove = false, target = id.value) {
  await run(async () => {
    const row: Record<string, unknown> = {};
    for (const field of fields.value)
      row[field] =
        field === "verses"
          ? String(draft[field] || "")
              .split("\n")
              .filter((v) => v.trim())
          : draft[field] || "";
    const payload = {
      kind: kind.value,
      id: target,
      row: remove ? null : row,
      revision: revision.value,
    };
    await api(
      "leisure/save",
      "POST",
      payload,
      auth.actor?.csrf,
      keyFor(payload),
    );
    editing.value = false;
    notice.value = remove ? "محتوا حذف شد." : "محتوا ذخیره شد.";
    await load();
  });
}
function remove(target: string) {
  if (confirm("این محتوا از ربات حذف شود؟")) void save(true, target);
}
onMounted(async () => {
  await auth.load();
  if (auth.can("content")) await run(load);
});
</script>
<template>
  <div class="catalog-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">چند خط، یک حال بهتر</p>
        <h1>تفریح و مطالعه</h1>
        <p class="muted">
          محتوای آمادهٔ ربات را ویرایش کن یا پیشنهاد تازه‌ای اضافه کن.
        </p>
      </div>
      <RouterLink class="button secondary" to="/manage">حساب مدیریت</RouterLink>
    </header>
    <p v-if="error" class="error-box" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice-box" role="status">{{ notice }}</p>
    <p v-if="!auth.can('content')" role="alert">
      اجازهٔ مدیریت این بخش را ندارید.
    </p>
    <template v-else
      ><div class="catalog-toolbar">
        <select
          v-model="kind"
          aria-label="نوع محتوای تفریح"
          :disabled="busy"
          @change="
            page = 0;
            editing = false;
          "
        >
          <option v-for="(label, k) in kinds" :key="k" :value="k">
            {{ label }}
          </option></select
        ><input
          v-model="search"
          type="search"
          aria-label="جست‌وجوی محتوای تفریح"
          placeholder="جست‌وجو در محتوا…"
          @input="page = 0"
        /><button class="button" :disabled="busy" @click="fresh">
          افزودن محتوا</button
        ><button class="button secondary" :disabled="busy" @click="run(load)">
          تازه‌سازی
        </button>
      </div>
      <button
        v-if="selected.length"
        class="button danger"
        :disabled="busy"
        @click="archiveSelection"
      >
        حذف انتخاب‌شده‌ها
      </button>
      <section class="panel catalog-list">
        <article
          v-for="[key, row] in rows.slice(page * 10, page * 10 + 10)"
          :key="key"
          class="catalog-row"
        >
          <input
            v-model="selected"
            type="checkbox"
            :value="key"
            :aria-label="
              'انتخاب ' + String(row.title || row.text || row.question || '')
            "
          />
          <div>
            <strong>{{
              String(row.title || row.text || row.question || "").slice(0, 100)
            }}</strong
            ><small>{{ row.name || row.author || row.level }}</small>
          </div>
          <button
            class="button secondary"
            :disabled="busy"
            @click="edit(key, row)"
          >
            ویرایش</button
          ><button class="button danger" :disabled="busy" @click="remove(key)">
            حذف
          </button>
        </article>
        <p v-if="!rows.length">محتوایی در این بخش نیست.</p>
        <div class="catalog-pagination">
          <button
            class="button secondary"
            :disabled="page === 0"
            @click="page--"
          >
            صفحه قبل</button
          ><span
            >{{ page + 1 }} از
            {{ Math.max(1, Math.ceil(rows.length / 10)) }}</span
          ><button
            class="button secondary"
            :disabled="(page + 1) * 10 >= rows.length"
            @click="page++"
          >
            صفحه بعد
          </button>
        </div>
      </section>
      <div v-if="editing" class="studio-layout">
        <form class="panel studio-editor" @submit.prevent="save()">
          <div class="section-heading">
            <h2>{{ kinds[kind] }}</h2>
            <button
              class="button secondary"
              type="button"
              @click="editing = false"
            >
              بستن فرم
            </button>
          </div>
          <label v-for="field in fields" :key="field"
            >{{ labels[field]
            }}<select
              v-if="field === 'poet'"
              v-model="draft[field]"
              :aria-label="labels[field]"
            >
              <option value="hafez">حافظ</option>
              <option value="saadi">سعدی</option>
              <option value="rumi">مولانا</option></select
            ><select
              v-else-if="field === 'level'"
              v-model="draft[field]"
              :aria-label="labels[field]"
            >
              <option value="easy">آسان</option>
              <option value="medium">متوسط</option>
              <option value="hard">سخت</option></select
            ><select
              v-else-if="field === 'category'"
              v-model="draft[field]"
              :aria-label="labels[field]"
            >
              <option value="short">کوتاه</option>
              <option value="iran">ایرانی</option>
              <option value="thought">معنا و اندیشه</option>
              <option value="classic">کلاسیک</option></select
            ><textarea
              v-else-if="
                [
                  'verses',
                  'interpretation',
                  'text',
                  'summary',
                  'why',
                  'answer',
                ].includes(field)
              "
              v-model="draft[field]"
              rows="5"
              maxlength="12000"
              :required="field !== 'interpretation' || draft.poet === 'hafez'"
            ></textarea
            ><input
              v-else
              v-model="draft[field]"
              maxlength="1000"
              required /></label
          ><button class="button" :disabled="busy">ذخیرهٔ محتوا</button>
        </form>
        <section class="telegram-preview" aria-label="پیش‌نمایش محتوای تفریح">
          <div class="preview-label">پیش‌نمایش · بدون ثبت</div>
          <article class="telegram-bubble">
            <h3>{{ draft.title || kinds[kind] }}</h3>
            <template v-for="field in fields" :key="field"
              ><p v-if="draft[field]" class="pre-wrap">
                {{ draft[field] }}
              </p></template
            >
          </article>
        </section>
      </div></template
    >
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, reactive, toRaw, computed } from "vue";
import { useAuth } from "../stores/auth";
import { domainRequest as api, commandKey } from "../lib/domain-api";
import {
  newBlock,
  newItem,
  blockNames,
  type RichDocument,
} from "../lib/studio";
import BlockPreview from "../components/BlockPreview.vue";
import type { components } from "../lib/api.generated";
type Content = components["schemas"]["Content"];
type Category = {
  id: string;
  name: string;
  active: boolean;
  revision: number;
  following: boolean;
};
type Plan = {
  contentId: string;
  version: number;
  categoryId: string;
  mode: string;
  count: number;
  maxUser: string;
};
const templateID = ref("");
const auth = useAuth(),
  kind = ref("announcements"),
  items = ref<Content[]>([]),
  categories = ref<Category[]>([]),
  templates = ref<
    { id: string; title: string; kind: string; document: RichDocument }[]
  >([]),
  dispatches = ref<
    { id: string; title: string; mode: string; state: string }[]
  >([]),
  error = ref(""),
  notice = ref(""),
  busy = ref(false),
  page = ref(0),
  editing = ref(false),
  plan = ref<Plan | null>(null),
  category = ref("0"),
  mode = ref("bank"),
  blockType = ref("text");
const draft = reactive<Content>({
    id: "0",
    kind: "announcements",
    title: "",
    body: "",
    metadata: { access: "public", audience_tag: "" },
    status: "draft",
    version: 0,
    publishedAt: null,
    expiresAt: null,
  }),
  document = reactive<RichDocument>({
    schema: 1,
    rich: true,
    blocks: [newBlock("text")],
  }),
  categoryForm = reactive<Category>({
    id: "0",
    name: "",
    active: true,
    revision: 0,
    following: false,
  });
let signature = "",
  key = "";
function keyFor(v: unknown) {
  const s = JSON.stringify(v);
  if (s !== signature) {
    signature = s;
    key = commandKey();
  }
  return key;
}
async function run(fn: () => Promise<void>) {
  if (busy.value) return;
  busy.value = true;
  error.value = "";
  notice.value = "";
  try {
    await fn();
  } catch (e) {
    error.value = (e as Error).message;
  } finally {
    busy.value = false;
  }
}
async function command<T>(path: string, body: unknown) {
  return api<T>(path, "POST", body, auth.actor?.csrf, keyFor({ path, body }));
}
async function load() {
  items.value = await api<Content[]>(
    `catalog?kind=${kind.value}&page=${page.value}`,
  );
  categories.value = await api<Category[]>("notices/categories");
  templates.value = await api("studio/templates");
  if (auth.can("campaigns")) dispatches.value = await api("notices/dispatches");
}
function fresh() {
  Object.assign(draft, {
    id: "0",
    kind: kind.value,
    title: "",
    body: "",
    status: "draft",
    metadata: { access: "public", audience_tag: "" },
    version: 0,
    publishedAt: null,
    expiresAt: null,
  });
  document.blocks = [newBlock("text")];
  category.value = "0";
  mode.value = "bank";
  signature = "";
  plan.value = null;
  editing.value = true;
}
async function edit(id: string) {
  await run(async () => {
    const v = await api<{
      content: Content;
      document: RichDocument | null;
      categoryId: string;
      mode: string;
    }>("studio?id=" + id);
    Object.assign(draft, v.content);
    Object.assign(
      document,
      v.document || {
        schema: 1,
        rich: true,
        blocks: [{ ...newBlock("text"), text: v.content.body }],
      },
    );
    category.value = v.categoryId;
    mode.value = v.mode;
    plan.value = null;
    signature = "";
    editing.value = true;
  });
}
async function save() {
  await run(async () => {
    const c = await command<Content>("studio/save", {
      content: toRaw(draft),
      document: toRaw(document),
      categoryId: category.value,
      mode: mode.value,
    });
    Object.assign(draft, c);
    notice.value = "پیام ذخیره شد؛ هنوز ارسال سراسری انجام نشده است.";
    plan.value = null;
    await load();
  });
}
async function previewDispatch() {
  await run(async () => {
    plan.value = await api<Plan>(
      `notices/preview?id=${draft.id}&mode=${mode.value}`,
    );
  });
}
async function send() {
  await run(async () => {
    await command("notices/dispatch", plan.value);
    plan.value = null;
    notice.value = "ارسال وارد صف شد.";
    await load();
  });
}
async function archive(c: Content) {
  if (!confirm("این پیام بایگانی شود؟")) return;
  await run(async () => {
    await command("catalog/archive", {
      items: [{ id: c.id, version: c.version }],
    });
    await load();
    if (c.id === draft.id) editing.value = false;
  });
}
function move(i: number, delta: number) {
  const j = i + delta;
  if (j < 0 || j >= document.blocks.length) return;
  const value = document.blocks[i]!;
  document.blocks.splice(i, 1);
  document.blocks.splice(j, 0, value);
}
async function upload(event: Event, i: number, slide = false) {
  const input = event.target as HTMLInputElement,
    files = input.files;
  if (!files?.length) return;
  await run(async () => {
    for (const file of files) {
      const data = new FormData();
      data.append("file", file);
      const response = await fetch("/api/v1/admin/files/upload", {
        method: "POST",
        headers: { "X-CSRF-Token": auth.actor?.csrf || "" },
        credentials: "same-origin",
        body: data,
      });
      if (!response.ok)
        throw new Error(
          "بارگذاری رسانه انجام نشد؛ نوع، حجم و دسترسی فایل را بررسی کنید.",
        );
      const result = await response.json();
      if (slide)
        document.blocks[i]!.slides.push({
          file_id: Number(result.id),
          caption: "",
        });
      else document.blocks[i]!.file_id = Number(result.id);
    }
  });
  input.value = "";
}
function applyTemplate(event: Event) {
  const id = (event.target as HTMLSelectElement).value;
  const t = templates.value.find((t) => t.id === id);
  if (!t) return;
  fresh();
  document.blocks = structuredClone(toRaw(t.document.blocks));
}
const canSend = computed(
  () =>
    auth.can("campaigns") &&
    auth.can("publish") &&
    draft.id !== "0" &&
    draft.status === "published" &&
    kind.value === "announcements",
);
onMounted(async () => {
  await auth.load();
  if (auth.can("content")) await run(load);
});
</script>
<template>
  <div class="catalog-page">
    <header class="page-heading">
      <div>
        <p class="eyebrow">پیام روشن، ارتباط بهتر</p>
        <h1>استودیوی پیام و اطلاعیه</h1>
        <p class="muted">
          پیش‌نمایش، انتشار در بانک و ارسال هدفمند، هرکدام با کنترل مشخص.
        </p>
      </div>
      <RouterLink class="button secondary" to="/manage">حساب مدیریت</RouterLink>
    </header>
    <p v-if="error" role="alert" class="error-box">{{ error }}</p>
    <p v-if="notice" role="status" class="notice-box">{{ notice }}</p>
    <p v-if="!auth.can('content')" role="alert">
      اجازهٔ مدیریت این بخش را ندارید.
    </p>
    <template v-else
      ><div class="catalog-toolbar">
        <label
          >نوع محتوا<select
            aria-label="نوع محتوا"
            v-model="kind"
            :disabled="busy"
            @change="
              page = 0;
              editing = false;
              run(load);
            "
          >
            <option value="announcements">اطلاعیه‌ها</option>
            <option value="news">خبرها</option>
            <option value="guides">راهنماها</option>
            <option value="resources">منابع</option>
            <option value="faq">پرسش‌های متداول</option>
          </select></label
        ><button class="button" :disabled="busy" @click="fresh">
          پیام جدید</button
        ><label
          >قالب آماده<select
            aria-label="قالب آماده"
            :disabled="busy"
            @change="
              fresh();
              document.blocks = [
                newBlock(($event.target as HTMLSelectElement).value),
              ];
            "
          >
            <option value="text">پیام متنی</option>
            <option value="table">جدول اطلاعات</option>
            <option value="slideshow">آلبوم تصویری</option>
            <option value="buttons">دکمه و تیک</option>
          </select></label
        ><select
          v-model="templateID"
          aria-label="قالب ذخیره‌شده"
          @change="applyTemplate"
        >
          <option value="">قالب‌های شما</option>
          <option v-for="t in templates" :key="t.id" :value="t.id">
            {{ t.title }}
          </option>
        </select>
      </div>
      <button
        v-if="templateID"
        class="button danger"
        :disabled="busy"
        @click="
          run(async () => {
            await command('studio/template/delete', { id: templateID });
            templateID = '';
            notice = 'قالب حذف شد.';
            await load();
          })
        "
      >
        حذف قالب انتخاب‌شده
      </button>
      <section class="panel catalog-list">
        <article v-for="c in items" :key="c.id" class="catalog-row">
          <div>
            <strong>{{ c.title }}</strong
            ><small>{{ c.status }} · نسخه {{ c.version }}</small>
          </div>
          <button class="button secondary" :disabled="busy" @click="edit(c.id)">
            ویرایش</button
          ><button class="button danger" :disabled="busy" @click="archive(c)">
            بایگانی
          </button>
        </article>
        <p v-if="!items.length">هنوز پیامی در این صفحه نیست.</p>
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
          ><span>صفحه {{ page + 1 }}</span
          ><button
            class="button secondary"
            :disabled="busy || items.length < 20"
            @click="
              page++;
              run(load);
            "
          >
            صفحه بعد
          </button>
        </div>
      </section>
      <div v-if="editing" class="studio-layout">
        <section class="panel studio-editor">
          <div class="section-heading">
            <h2>ساخت پیام</h2>
            <button class="button secondary" @click="editing = false">
              بستن فرم
            </button>
          </div>
          <label
            >عنوان پیام<input v-model="draft.title" maxlength="200" /></label
          ><label
            >وضعیت انتشار<select
              aria-label="وضعیت انتشار"
              v-model="draft.status"
            >
              <option value="draft">پیش‌نویس</option>
              <option value="pending">در انتظار بررسی</option>
              <option v-if="auth.can('publish')" value="published">
                منتشرشده
              </option>
              <option value="archived">بایگانی</option>
            </select></label
          ><label v-if="kind === 'announcements'"
            >دستهٔ اطلاعیه<select aria-label="دستهٔ اطلاعیه" v-model="category">
              <option value="0">بدون دسته</option>
              <option
                v-for="c in categories.filter((c) => c.active)"
                :key="c.id"
                :value="c.id"
              >
                {{ c.name }}
              </option>
            </select></label
          ><label v-if="kind === 'announcements'"
            >روش توزیع<select aria-label="روش توزیع" v-model="mode">
              <option value="bank">فقط بانک اطلاعیه‌ها</option>
              <option value="all">بانک و پیام سراسری</option>
              <option value="followers">بانک و دنبال‌کنندگان دسته</option>
            </select></label
          >
          <article
            v-for="(b, i) in document.blocks"
            :key="i"
            class="block-editor"
          >
            <div class="section-heading">
              <strong>{{ i + 1 }}. {{ blockNames[b.type] || b.type }}</strong>
              <div>
                <button
                  type="button"
                  aria-label="بلوک بالاتر"
                  :disabled="i === 0"
                  @click="move(i, -1)"
                >
                  ↑</button
                ><button
                  type="button"
                  aria-label="بلوک پایین‌تر"
                  :disabled="i === document.blocks.length - 1"
                  @click="move(i, 1)"
                >
                  ↓</button
                ><button
                  class="danger"
                  type="button"
                  aria-label="حذف بلوک"
                  @click="document.blocks.splice(i, 1)"
                >
                  حذف
                </button>
              </div>
            </div>
            <label
              ><input v-model="b.enabled" type="checkbox" /> نمایش بلوک</label
            ><label>تیتر بلوک<input v-model="b.title" maxlength="200" /></label
            ><label
              >توضیح بلوک<textarea
                v-model="b.description"
                maxlength="2000"
                rows="2"
              ></textarea></label
            ><template
              v-if="['text', 'heading', 'typing', 'details'].includes(b.type)"
              ><div class="format-toolbar">
                <button type="button" @click="b.text += '<b>متن پررنگ</b>'">
                  B</button
                ><button type="button" @click="b.text += '<i>متن مورب</i>'">
                  I</button
                ><button
                  type="button"
                  @click="
                    b.text +=
                      '<a href=&quot;https://example.org&quot;>متن پیوند</a>'
                  "
                >
                  پیوند</button
                ><button
                  type="button"
                  @click="b.text += '<tg-spoiler>متن پوشیده</tg-spoiler>'"
                >
                  پوشیده
                </button>
              </div>
              <label
                >متن بلوک<textarea
                  v-model="b.text"
                  rows="5"
                  maxlength="12000"
                ></textarea></label
            ></template>
            <template v-if="b.type === 'table'"
              ><div class="table-scroll">
                <table>
                  <tbody>
                    <tr v-for="(row, r) in b.rows" :key="r">
                      <td v-for="(_, c) in row" :key="c">
                        <input
                          v-model="row[c]"
                          :aria-label="`سلول ${r + 1} ${c + 1}`"
                          maxlength="500"
                        />
                      </td>
                      <td>
                        <button
                          type="button"
                          :disabled="b.rows.length <= 1"
                          @click="b.rows.splice(r, 1)"
                        >
                          −
                        </button>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <button
                type="button"
                :disabled="b.rows.length >= 30"
                @click="b.rows.push(Array(b.rows[0]?.length || 2).fill(''))"
              >
                افزودن سطر</button
              ><button
                type="button"
                :disabled="(b.rows[0]?.length || 0) >= 10"
                @click="b.rows.forEach((r) => r.push(''))"
              >
                افزودن ستون</button
              ><button
                type="button"
                :disabled="(b.rows[0]?.length || 0) <= 1"
                @click="b.rows.forEach((r) => r.pop())"
              >
                حذف ستون</button
              ><label
                ><input v-model="b.header" type="checkbox" /> سطر اول عنوان
                ستون‌هاست</label
              ></template
            >
            <template v-if="['image', 'slideshow', 'audio'].includes(b.type)"
              ><label v-if="auth.can('files')"
                >بارگذاری رسانه<input
                  type="file"
                  :accept="
                    b.type === 'audio'
                      ? 'audio/*'
                      : 'image/png,image/jpeg,image/webp,image/gif'
                  "
                  :multiple="b.type === 'slideshow'"
                  :disabled="busy"
                  @change="upload($event, i, b.type === 'slideshow')" /></label
              ><label v-if="b.type !== 'slideshow'"
                >شناسهٔ فایل موجود<input
                  v-model.number="b.file_id"
                  type="number"
                  min="1" /></label
              ><label v-if="b.type === 'audio'"
                ><input v-model="b.voice" type="checkbox" /> ارسال به‌صورت ویس
                OGG/Opus</label
              >
              <p v-if="b.type === 'slideshow'">
                ۲ تا ۱۰ تصویر؛ هر تصویر یک کپشن مستقل دارد.
              </p>
              <div v-for="(s, j) in b.slides" :key="j">
                <input
                  v-model.number="s.file_id"
                  type="number"
                  aria-label="شناسه تصویر"
                /><input
                  v-model="s.caption"
                  aria-label="کپشن تصویر"
                  maxlength="1000"
                /><button type="button" @click="b.slides.splice(j, 1)">
                  حذف تصویر
                </button>
              </div></template
            >
            <template v-if="b.type === 'formula'"
              ><label
                >فرمول LaTeX<textarea
                  v-model="b.latex"
                  dir="ltr"
                ></textarea></label
              ><label
                >مثال<textarea v-model="b.example"></textarea></label></template
            ><label v-if="b.type === 'chart'"
              >دادهٔ نمودار<textarea
                :value="JSON.stringify(b.chart, null, 2)"
                dir="ltr"
                @change="
                  (() => {
                    try {
                      b.chart = JSON.parse(
                        ($event.target as HTMLTextAreaElement).value,
                      );
                      error = '';
                    } catch {
                      error = 'ساختار دادهٔ نمودار معتبر نیست.';
                    }
                  })()
                "
              ></textarea>
            </label>
            <template v-if="b.type === 'buttons'"
              ><label
                >تعداد ستون‌ها<input
                  v-model.number="b.columns"
                  type="number"
                  min="1"
                  max="4"
              /></label>
              <fieldset
                v-for="(item, j) in b.items"
                :key="j"
                class="action-editor"
              >
                <legend>دکمه {{ j + 1 }}</legend>
                <label
                  >عنوان دکمه<input v-model="item.text" maxlength="60" /></label
                ><label
                  >عملکرد<select aria-label="عملکرد" v-model="item.action">
                    <option value="url">باز کردن پیوند</option>
                    <option value="copy">کپی متن</option>
                    <option value="menu">رفتن به منو</option>
                    <option value="content">باز کردن مطلب</option>
                    <option value="toggle">تیک روشن/خاموش</option>
                    <option value="confirm">تأیید</option>
                    <option value="cancel">لغو</option>
                    <option value="increment">افزایش شمارنده</option>
                    <option value="decrement">کاهش شمارنده</option>
                  </select></label
                ><label
                  >مقصد یا مقدار<input
                    v-model="item.value"
                    dir="auto"
                    maxlength="500" /></label
                ><label
                  >رنگ<select aria-label="رنگ" v-model="item.style">
                    <option value="primary">آبی</option>
                    <option value="success">سبز</option>
                    <option value="danger">قرمز</option>
                    <option value="default">خاکستری</option>
                  </select></label
                ><template
                  v-if="['increment', 'decrement'].includes(item.action)"
                  ><label
                    >گام<input
                      v-model.number="item.step"
                      type="number"
                      min="1"
                      max="100" /></label
                  ><label
                    >کمینه<input
                      v-model.number="item.min"
                      type="number" /></label
                  ><label
                    >بیشینه<input
                      v-model.number="item.max"
                      type="number" /></label></template
                ><label
                  ><input v-model="item.row_break" type="checkbox" /> شروع ردیف
                  تازه</label
                ><label
                  ><input v-model="item.disabled" type="checkbox" />
                  غیرفعال</label
                ><button
                  type="button"
                  class="danger"
                  @click="b.items.splice(j, 1)"
                >
                  حذف دکمه
                </button>
              </fieldset>
              <button
                type="button"
                :disabled="b.items.length >= 20"
                @click="b.items.push(newItem())"
              >
                افزودن دکمه
              </button></template
            >
          </article>
          <div class="catalog-toolbar">
            <select v-model="blockType" aria-label="نوع بلوک جدید">
              <option
                v-for="(name, type) in blockNames"
                :key="type"
                :value="type"
              >
                {{ name }}
              </option></select
            ><button
              class="button secondary"
              :disabled="document.blocks.length >= 40"
              @click="document.blocks.push(newBlock(blockType))"
            >
              افزودن بلوک
            </button>
          </div>
          <p class="muted">
            جدول، متن و رسانه در نمایش استاندارد تلگرام به پیام‌های سازگار تبدیل
            می‌شوند. ظاهر نهایی به نسخهٔ تلگرام کاربر وابسته است.
          </p>
          <div class="catalog-toolbar">
            <button
              class="button"
              :disabled="busy || !draft.title"
              @click="save"
            >
              ذخیرهٔ پیام</button
            ><button
              class="button secondary"
              :disabled="busy || !draft.title"
              @click="
                run(async () => {
                  await command('studio/template', {
                    title: draft.title,
                    kind,
                    document: toRaw(document),
                  });
                  notice = 'قالب ذخیره شد.';
                  await load();
                })
              "
            >
              ذخیره به‌عنوان قالب</button
            ><button
              v-if="canSend"
              class="button secondary"
              :disabled="busy || mode === 'bank'"
              @click="previewDispatch"
            >
              پیش‌نمایش مخاطبان
            </button>
          </div>
          <section
            v-if="plan"
            class="notice-box"
            role="region"
            aria-label="تأیید ارسال"
          >
            <p>{{ plan.count }} مخاطب · سقف هر ارسال ۱۵۰۰ نفر</p>
            <p>نسخهٔ {{ plan.version }} از پیام ذخیره‌شده ارسال می‌شود.</p>
            <button class="button" :disabled="busy" @click="send">
              تأیید و ورود به صف</button
            ><button class="button secondary" @click="plan = null">
              لغو ارسال
            </button>
          </section>
        </section>
        <BlockPreview :document="document" :title="draft.title" />
      </div>
      <section v-if="kind === 'announcements'" class="panel">
        <h2>دسته‌های اطلاعیه</h2>
        <form
          class="catalog-toolbar"
          @submit.prevent="
            run(async () => {
              await command('notices/category', categoryForm);
              Object.assign(categoryForm, {
                id: '0',
                name: '',
                active: true,
                revision: 0,
                following: false,
              });
              await load();
            })
          "
        >
          <label
            >نام دسته<input
              v-model="categoryForm.name"
              required
              maxlength="100" /></label
          ><label
            ><input v-model="categoryForm.active" type="checkbox" /> فعال</label
          ><button class="button secondary" :disabled="busy">ذخیره دسته</button>
        </form>
        <button
          v-for="c in categories"
          :key="c.id"
          class="category-chip"
          @click="Object.assign(categoryForm, c)"
        >
          {{ c.name }} {{ c.active ? "" : "(غیرفعال)" }}
        </button>
      </section>
      <section v-if="auth.can('campaigns')" class="panel">
        <h2>ارسال‌های اخیر</h2>
        <article v-for="d in dispatches" :key="d.id" class="catalog-row">
          <span>{{ d.title }} · {{ d.mode }} · {{ d.state }}</span
          ><button
            v-if="d.state !== 'cancelled'"
            class="button danger"
            :disabled="busy"
            @click="
              run(async () => {
                await command('notices/cancel', { id: d.id });
                await load();
              })
            "
          >
            توقف ارسال
          </button>
        </article>
      </section></template
    >
  </div>
</template>

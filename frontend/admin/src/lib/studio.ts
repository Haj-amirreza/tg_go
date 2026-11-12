export type RichItem = {
  text: string;
  action: string;
  value: string;
  style: string;
  icon: string;
  custom_emoji_id: string;
  disabled: boolean;
  row_break: boolean;
  step: number;
  min: number;
  max: number;
  audience_tag: string;
};
export type RichBlock = {
  type: string;
  enabled: boolean;
  title: string;
  description: string;
  text: string;
  size: number;
  open: boolean;
  rows: string[][];
  header: boolean;
  bordered: boolean;
  striped: boolean;
  compact: boolean;
  file_id: number;
  voice: boolean;
  slides: { file_id: number; caption: string }[];
  items: RichItem[];
  columns: number;
  latex: string;
  inline: boolean;
  variables: Record<string, unknown>[];
  example: string;
  calculator: string;
  result_unit: string;
  chart: unknown;
  quiz_id: number;
  speed: string;
  animate: boolean;
  min_length: number;
  max_length: number;
};
export type RichDocument = {
  schema: number;
  rich: boolean;
  blocks: RichBlock[];
};
export const blockNames: Record<string, string> = {
  text: "متن و پیوند",
  heading: "تیتر",
  table: "جدول",
  image: "تصویر",
  slideshow: "آلبوم تصاویر",
  audio: "صوت و ویس",
  typing: "متن تایپی",
  formula: "فرمول",
  chart: "نمودار",
  details: "متن بازشونده",
  buttons: "دکمه و تیک",
};
export const newItem = (): RichItem => ({
  text: "خواندم",
  action: "toggle",
  value: "",
  style: "success",
  icon: "",
  custom_emoji_id: "",
  disabled: false,
  row_break: false,
  step: 1,
  min: 0,
  max: 100,
  audience_tag: "",
});
export function newBlock(type: string): RichBlock {
  return {
    type,
    enabled: true,
    title: "",
    description: "",
    text: "",
    size: 2,
    open: false,
    rows: [
      ["عنوان", "مقدار"],
      ["", ""],
    ],
    header: true,
    bordered: true,
    striped: true,
    compact: true,
    file_id: 0,
    voice: false,
    slides: [],
    items: [newItem()],
    columns: 2,
    latex: "",
    inline: false,
    variables: [],
    example: "",
    calculator: "",
    result_unit: "",
    chart: { labels: ["الف", "ب"], values: [1, 2] },
    quiz_id: 0,
    speed: "balanced",
    animate: true,
    min_length: 30,
    max_length: 4000,
  };
}
export function safeRichHTML(value: string): string {
  const doc = new DOMParser().parseFromString(value, "text/html"),
    box = document.createElement("div");
  const allowed = new Set([
    "B",
    "STRONG",
    "I",
    "EM",
    "U",
    "INS",
    "S",
    "DEL",
    "CODE",
    "PRE",
    "TG-SPOILER",
    "SUP",
    "SUB",
    "BR",
    "A",
  ]);
  function copy(source: Node, parent: Node) {
    if (source.nodeType === Node.TEXT_NODE) {
      parent.appendChild(document.createTextNode(source.textContent || ""));
      return;
    }
    if (!(source instanceof Element)) return;
    if (!allowed.has(source.tagName)) {
      parent.appendChild(document.createTextNode(source.textContent || ""));
      return;
    }
    const node = document.createElement(source.tagName.toLowerCase());
    if (source.tagName === "A") {
      try {
        const u = new URL(source.getAttribute("href") || "");
        if (u.protocol === "https:" && !u.username && !u.password) {
          node.setAttribute("href", u.href);
          node.setAttribute("target", "_blank");
          node.setAttribute("rel", "noopener noreferrer");
        }
      } catch {}
    }
    for (const child of source.childNodes) copy(child, node);
    parent.appendChild(node);
  }
  for (const node of doc.body.childNodes) copy(node, box);
  return box.innerHTML;
}

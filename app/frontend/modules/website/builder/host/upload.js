import { authenticatedHeaders, buildBackendURL } from "@/services/api/jsonv2.js";

// Uploads an image through the attachment endpoint and returns its public URL.
const IMAGE_TYPES = ["image/png", "image/jpeg", "image/gif", "image/webp"];

export async function uploadImage(file) {
  if (!IMAGE_TYPES.includes(file.type)) throw new Error(`"${file.name}" is not a supported image type.`);
  const form = new FormData();
  form.append("file", file, file.name);
  form.append("name", file.name);
  form.append("is_public", "true");
  const headers = authenticatedHeaders();
  delete headers["Content-Type"];
  const response = await fetch(buildBackendURL("/web/content"), { method: "POST", headers, body: form });
  const payload = await response.json().catch(() => null);
  if (!response.ok) throw new Error(`Could not upload "${file.name}".`);
  const envelope = payload && typeof payload === "object" && "result" in payload ? payload.result : payload;
  const record = Array.isArray(envelope) ? envelope[0] : envelope;
  const id = Number(record?.id);
  if (!Number.isInteger(id) || id <= 0) throw new Error("Upload did not return an attachment id.");
  return `/web/content/${id}`;
}

// The real .openai/hosting.json carries the Site's project_id and is written when the Site is published. Local builds and checks start from the template when it is missing.
import { copyFileSync, existsSync } from "node:fs";

const target = new URL("../.openai/hosting.json", import.meta.url);
if (!existsSync(target)) copyFileSync(new URL("../.openai/hosting.template.json", import.meta.url), target);

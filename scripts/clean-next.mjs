import { rm } from "node:fs/promises";
import path from "node:path";

await rm(path.join(process.cwd(), ".next"), { recursive: true, force: true, maxRetries: 5, retryDelay: 250 });
console.log("Removed .next cache");

// Google Search Console ownership check. The file lives in src/assets (not published
// by Astro) and is served here at the root, where Google looks for it.
import verification from '../assets/google304cfd3bc53bbbb4.html?raw'

export function GET() {
  return new Response(verification, { headers: { 'Content-Type': 'text/html; charset=utf-8' } })
}

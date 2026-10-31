// The Cloudflare Worker of https://padmate.download/ (ADR-0078).
//
// The site has no web page. This Worker is routed on the path "/" and nothing
// else (wrangler.toml), so that the address opens the newest installer: it
// reads /latest.yml, the file the installed application's update reads too, and
// answers with a redirect to the installer that file names. /latest.yml, the
// installers and their .sha256 files are served by R2 itself, never by this
// Worker; a route on /* would put it in front of the application's updates.
//
// The redirect is a 302 and is not cached: a 301 would pin an old installer in
// browsers. latest.yml is kept at the edge for a minute only.
// app/scripts/publish-r2.mjs puts the release on the site and reads this
// redirect back.
//
// The Worker is deployed by hand from this folder:
//   npx wrangler@4.151.1 deploy

/** The installers of padmate; latest.yml is trusted for nothing else. */
export const INSTALLER =
  /^padmate-v\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?-windows-amd64-setup\.exe$/;

/**
 * The installer the text of latest.yml names in its `path:` line, or null when
 * there is none or it is not the name of a padmate installer.
 *
 * @param {string} text
 * @returns {string | null}
 */
export function installerFromLatest(text) {
  const name = /^path:\s*(\S+)\s*$/m.exec(text)?.[1];
  return name !== undefined && INSTALLER.test(name) ? name : null;
}

const unavailable = () =>
  new Response(
    "padmate: the download is not available right now. Please try again later.",
    {
      status: 503,
      headers: {
        "content-type": "text/plain; charset=utf-8",
        "cache-control": "no-store",
      },
    },
  );

/**
 * The answer to a request for the address of the site.
 *
 * @param {Request} request
 * @param {(url: URL, init: { cf: { cacheTtl: number } }) => Promise<Response>} fetchLatest
 * @returns {Promise<Response>}
 */
export async function respond(request, fetchLatest) {
  // Always https, also for a visitor who came over http: Chrome warns about,
  // and may block, a download that is not over https.
  const here = new URL(request.url);
  here.protocol = "https:";
  let name = null;
  try {
    const res = await fetchLatest(new URL("/latest.yml", here), {
      cf: { cacheTtl: 60 },
    });
    if (res.ok) name = installerFromLatest(await res.text());
  } catch {
    // The same answer as a latest.yml that names nothing.
  }
  if (name === null) return unavailable();
  return new Response(null, {
    status: 302,
    headers: {
      location: new URL(`/${name}`, here).href,
      "cache-control": "no-cache",
    },
  });
}

export default {
  fetch(request) {
    return respond(request, (url, init) => fetch(url, init));
  },
};

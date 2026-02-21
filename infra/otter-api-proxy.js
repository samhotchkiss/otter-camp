export default {
  async fetch(request) {
    const url = new URL(request.url);
    url.hostname = "api.otter.camp";
    return fetch(new Request(url, request));
  },
};

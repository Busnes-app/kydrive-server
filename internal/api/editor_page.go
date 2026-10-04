package api

import (
	"io"
	"net/http"
)

func (s *Server) editorPage(w http.ResponseWriter, r *http.Request) {
	if s.editor == nil {
		s.writeError(w, 503, "Euro-Office is not configured")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' "+s.editor.Origin+"; style-src 'self' 'unsafe-inline'; frame-src "+s.editor.Origin+"; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'")
	io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>KyDrive · Euro-Office</title><style>html,body,#editor{height:100%;margin:0}#message{padding:24px;font:16px system-ui}</style></head><body><div id="message" role="status">Opening document…</div><div id="editor"></div><script src="/editor-bootstrap.js"></script></body></html>`)
}
func (s *Server) editorScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, `(async()=>{const message=document.getElementById('message');try{const id=new URLSearchParams(location.search).get('file');if(!id)throw Error('No document selected');const csrf=document.cookie.split('; ').find(x=>x.startsWith('ky_csrf='));const resp=await fetch('/api/drive/files/'+encodeURIComponent(id)+'/editor',{method:'POST',headers:{'X-CSRF-Token':csrf?decodeURIComponent(csrf.slice(8)):''}});const data=await resp.json();if(!resp.ok)throw Error(data.error||'Editor unavailable');const script=document.createElement('script');script.src=data.origin+'/web-apps/apps/api/documents/api.js';await new Promise((resolve,reject)=>{script.onload=resolve;script.onerror=()=>reject(Error('Euro-Office could not be reached'));document.head.append(script)});data.config.events={onError:e=>{message.hidden=false;message.textContent='Euro-Office error: '+JSON.stringify(e.data)}};message.hidden=true;new DocsAPI.DocEditor('editor',data.config);}catch(e){message.textContent=e instanceof Error?e.message:'Editor unavailable';}})();`)
}

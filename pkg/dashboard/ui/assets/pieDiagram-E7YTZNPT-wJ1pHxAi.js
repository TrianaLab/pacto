import"./src-aspElOHd.js";import{m as e}from"./dist-LC_WpRiC.js";import{t}from"./arc-DGtfpWgF.js";import{i as n,p as r}from"./chunk-75Z2AOVW-DaeQeMDB.js";import{S as i,i as a,r as o}from"./index-DWMrjGcM.js";import{n as s}from"./mermaid-parser.core-CamCf686.js";import{n as c}from"./chunk-Y2CYZVJY-DsF7k-Jl.js";import{t as l}from"./chunk-X3CZISLH-DQsPyZY-.js";import{H as u,K as d,U as f,a as p,c as m,f as h,v as g,w as _,x as v,y}from"./chunk-DU6HZSFF-Dz2ArzqW.js";import{t as b}from"./chunk-CLGD4ZFX-C10JDrq4.js";import{t as x}from"./chunk-JWPE2WC7-DVXcaiue.js";function S(e,t){return t<e?-1:t>e?1:t>=e?0:NaN}function C(e){return e}function w(){var t=C,n=S,r=null,i=a(0),s=a(e),c=a(0);function l(a){var l,u=(a=o(a)).length,d,f,p=0,m=Array(u),h=Array(u),g=+i.apply(this,arguments),_=Math.min(e,Math.max(-e,s.apply(this,arguments)-g)),v,y=Math.min(Math.abs(_)/u,c.apply(this,arguments)),b=y*(_<0?-1:1),x;for(l=0;l<u;++l)(x=h[m[l]=l]=+t(a[l],l,a))>0&&(p+=x);for(n==null?r!=null&&m.sort(function(e,t){return r(a[e],a[t])}):m.sort(function(e,t){return n(h[e],h[t])}),l=0,f=p?(_-u*b)/p:0;l<u;++l,g=v)d=m[l],x=h[d],v=g+(x>0?x*f:0)+b,h[d]={data:a[d],index:l,value:x,startAngle:g,endAngle:v,padAngle:y};return h}return l.value=function(e){return arguments.length?(t=typeof e==`function`?e:a(+e),l):t},l.sortValues=function(e){return arguments.length?(n=e,r=null,l):n},l.sort=function(e){return arguments.length?(r=e,n=null,l):r},l.startAngle=function(e){return arguments.length?(i=typeof e==`function`?e:a(+e),l):i},l.endAngle=function(e){return arguments.length?(s=typeof e==`function`?e:a(+e),l):s},l.padAngle=function(e){return arguments.length?(c=typeof e==`function`?e:a(+e),l):c},l}var T=h.pie,E={sections:new Map,showData:!1,config:T},D=E.sections,O=E.showData,k=structuredClone(T),A={getConfig:c(()=>structuredClone(k),`getConfig`),clear:c(()=>{D=new Map,O=E.showData,p()},`clear`),setDiagramTitle:d,getDiagramTitle:_,setAccTitle:f,getAccTitle:y,setAccDescription:u,getAccDescription:g,addSection:c(({label:e,value:t})=>{if(t<0)throw Error(`"${e}" has invalid value: ${t}. Negative values are not allowed in pie charts. All slice values must be >= 0.`);D.has(e)||(D.set(e,t),l.debug(`added new section: ${e}, with value: ${t}`))},`addSection`),getSections:c(()=>D,`getSections`),setShowData:c(e=>{O=e},`setShowData`),getShowData:c(()=>O,`getShowData`)},j=c((e,t)=>{x(e,t),t.setShowData(e.showData),e.sections.map(t.addSection)},`populateDb`),M={parse:c(async e=>{let t=await s(`pie`,e);l.debug(t),j(t,A)},`parse`)},N=c(e=>`
  .pieCircle{
    stroke: ${e.pieStrokeColor};
    stroke-width : ${e.pieStrokeWidth};
    opacity : ${e.pieOpacity};
  }
  .pieCircle.highlighted{
    scale: 1.05;
    opacity: 1;
  }
  .pieCircle.highlightedOnHover:hover{
    transition-duration: 250ms;
    scale: 1.05;
    opacity: 1;
  }
  .pieOuterCircle{
    stroke: ${e.pieOuterStrokeColor};
    stroke-width: ${e.pieOuterStrokeWidth};
    fill: none;
  }
  .pieTitleText {
    text-anchor: middle;
    font-size: ${e.pieTitleTextSize};
    fill: ${e.pieTitleTextColor};
    font-family: ${e.fontFamily};
  }
  .slice {
    font-family: ${e.fontFamily};
    fill: ${e.pieSectionTextColor};
    font-size:${e.pieSectionTextSize};
    // fill: white;
  }
  .legend text {
    fill: ${e.pieLegendTextColor};
    font-family: ${e.fontFamily};
    font-size: ${e.pieLegendTextSize};
  }
`,`getStyles`),P=c(e=>{let t=[...e.values()].reduce((e,t)=>e+t,0),n=[...e.entries()].map(([e,t])=>({label:e,value:t})).filter(e=>e.value/t*100>=1);return w().value(e=>e.value).sort(null)(n)},`createPieArcs`),F={parser:M,db:A,renderer:{draw:c((e,a,o,s)=>{l.debug(`rendering pie chart
`+e);let c=s.db,u=v(),d=n(c.getConfig(),u.pie),f=b(a),p=f.append(`g`);p.attr(`transform`,`translate(225,225)`);let{themeVariables:h}=u,[g]=r(h.pieOuterStrokeWidth);g??=2;let _=d.legendPosition,y=d.textPosition,x=d.donutHole>0&&d.donutHole<=.9?d.donutHole:0,S=t().innerRadius(x*185).outerRadius(185),C=t().innerRadius(185*y).outerRadius(185*y),w=p.append(`g`);w.append(`circle`).attr(`cx`,0).attr(`cy`,0).attr(`r`,185+g/2).attr(`class`,`pieOuterCircle`);let T=c.getSections(),E=P(T),D=[h.pie1,h.pie2,h.pie3,h.pie4,h.pie5,h.pie6,h.pie7,h.pie8,h.pie9,h.pie10,h.pie11,h.pie12],O=0;T.forEach(e=>{O+=e});let k=E.filter(e=>(e.data.value/O*100).toFixed(0)!==`0`),A=i(D).domain([...T.keys()]);w.selectAll(`mySlices`).data(k).enter().append(`path`).attr(`d`,S).attr(`fill`,e=>A(e.data.label)).attr(`class`,e=>{let t=`pieCircle`;return d.highlightSlice===`hover`?t+=` highlightedOnHover`:d.highlightSlice===e.data.label&&(t+=` highlighted`),t}),w.selectAll(`mySlices`).data(k).enter().append(`text`).text(e=>(e.data.value/O*100).toFixed(0)+`%`).attr(`transform`,e=>`translate(`+C.centroid(e)+`)`).style(`text-anchor`,`middle`).attr(`class`,`slice`);let j=p.append(`text`).text(c.getDiagramTitle()).attr(`x`,0).attr(`y`,-400/2).attr(`class`,`pieTitleText`),M=[...T.entries()].map(([e,t])=>({label:e,value:t})),N=p.selectAll(`.legend`).data(M).enter().append(`g`).attr(`class`,`legend`);N.append(`rect`).attr(`width`,18).attr(`height`,18).style(`fill`,e=>A(e.label)).style(`stroke`,e=>A(e.label)),N.append(`text`).attr(`x`,22).attr(`y`,14).text(e=>c.getShowData()?`${e.label} [${e.value}]`:e.label);let F=Math.max(...N.selectAll(`text`).nodes().map(e=>e?.getBoundingClientRect().width??0)),I=450,L=490,R=M.length*22;switch(_){case`center`:N.attr(`transform`,(e,t)=>{let n=22*M.length/2,r=-F/2-22,i=t*22-n;return`translate(`+r+`,`+i+`)`});break;case`top`:I+=R,N.attr(`transform`,(e,t)=>`translate(${-F/2-22}, ${t*22-185})`),w.attr(`transform`,()=>`translate(0, ${R+22})`);break;case`bottom`:I+=R,N.attr(`transform`,(e,t)=>{let n=-F/2-22,r=t*22- -207;return`translate(`+n+`,`+r+`)`});break;case`left`:L+=22+F,N.attr(`transform`,(e,t)=>{let n=22*M.length/2;return`translate(-207,`+(t*22-n)+`)`}),w.attr(`transform`,()=>`translate(${F+18+4}, 0)`);break;default:L+=22+F,N.attr(`transform`,(e,t)=>{let n=22*M.length/2;return`translate(216,`+(t*22-n)+`)`});break}let z=j.node()?.getBoundingClientRect().width??0,B=450/2-z/2,V=450/2+z/2,H=Math.min(0,B),U=Math.max(L,V)-H;f.attr(`viewBox`,`${H} 0 ${U} ${I}`),m(f,I,U,d.useMaxWidth)},`draw`)},styles:N};export{F as diagram};
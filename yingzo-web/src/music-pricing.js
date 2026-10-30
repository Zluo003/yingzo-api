import { t as tr } from "./i18n/index.js";
const musicPriceLabels = {
  get "instrumental"() {
    return tr("纯音乐");
  },
  get "song"() {
    return tr("歌曲");
  },
  get "addInstrumental"() {
    return tr("添加伴奏");
  },
  get "addStem"() {
    return tr("添加音轨 (add stem)");
  },
  get "addVocals"() {
    return tr("添加人声");
  },
  get "adjustSpeed"() {
    return tr("调整速度");
  },
  get "alignedLyrics"() {
    return tr("歌词时间轴");
  },
  get "bpm"() {
    return tr("BPM 分析");
  },
  get "concat"() {
    return tr("完整歌曲合成 / 拼接");
  },
  get "coverSong"() {
    return tr("风格翻唱");
  },
  get "create_model"() {
    return tr("创建自定义音色模型");
  },
  get "createVoice"() {
    return tr("创建语音");
  },
  get "crop"() {
    return tr("裁剪音频");
  },
  get "download"() {
    return tr("下载音频文件");
  },
  get "extend"() {
    return tr("续写延长");
  },
  get "fadeIn"() {
    return tr("淡入");
  },
  get "fadeOut"() {
    return tr("淡出");
  },
  get "generateMp4"() {
    return tr("生成音乐视频 (MV)");
  },
  get "inspo"() {
    return tr("灵感生成 (inspo)");
  },
  get "lyrics"() {
    return tr("生成歌词");
  },
  get "mashup"() {
    return tr("生成混搭 (mashup)");
  },
  get "midi"() {
    return tr("生成 MIDI");
  },
  get "persona"() {
    return tr("创建歌手");
  },
  get "remaster"() {
    return tr("母带优化");
  },
  get "removeSection"() {
    return tr("删除片段");
  },
  get "replaceMusic"() {
    return tr("段落替换");
  },
  get "sample"() {
    return tr("样本转歌曲");
  },
  get "sounds"() {
    return tr("音效生成");
  },
  get "stems"() {
    return tr("分轨提取");
  },
  get "stemsAll"() {
    return tr("全量分轨");
  },
  get "upload_cover"() {
    return tr("上传音频翻唱");
  },
  get "uploadExtend"() {
    return tr("上传音频并延伸");
  },
  get "upload"() {
    return tr("上传音频");
  },
  get "upsampleTags"() {
    return tr("标签增强");
  },
  get "vox"() {
    return tr("提取 Vox（已弃用）");
  },
  get "instrumental:v6-wild"() {
    return tr("纯音乐 · v6-wild");
  },
  get "instrumental:v6-mini"() {
    return tr("纯音乐 · v6-mini");
  },
  get "instrumental:custom"() {
    return tr("纯音乐 · 自定义模型");
  },
  get "song:v6-wild"() {
    return tr("歌曲 · v6-wild");
  },
  get "song:v6-mini"() {
    return tr("歌曲 · v6-mini");
  },
  get "song:custom"() {
    return tr("歌曲 · 自定义模型");
  },
  get "addInstrumental:v6-wild"() {
    return tr("添加伴奏 · v6-wild");
  },
  get "addInstrumental:v6-mini"() {
    return tr("添加伴奏 · v6-mini");
  },
  get "addInstrumental:custom"() {
    return tr("添加伴奏 · 自定义模型");
  },
  get "addStem:v6-wild"() {
    return tr("添加音轨 (add stem) · v6-wild");
  },
  get "addStem:v6-mini"() {
    return tr("添加音轨 (add stem) · v6-mini");
  },
  get "addStem:custom"() {
    return tr("添加音轨 (add stem) · 自定义模型");
  },
  get "addVocals:v6-wild"() {
    return tr("添加人声 · v6-wild");
  },
  get "addVocals:v6-mini"() {
    return tr("添加人声 · v6-mini");
  },
  get "addVocals:custom"() {
    return tr("添加人声 · 自定义模型");
  },
  get "coverSong:v6-wild"() {
    return tr("风格翻唱 · v6-wild");
  },
  get "coverSong:v6-mini"() {
    return tr("风格翻唱 · v6-mini");
  },
  get "coverSong:custom"() {
    return tr("风格翻唱 · 自定义模型");
  },
  get "extend:v6-wild"() {
    return tr("续写延长 · v6-wild");
  },
  get "extend:v6-mini"() {
    return tr("续写延长 · v6-mini");
  },
  get "extend:custom"() {
    return tr("续写延长 · 自定义模型");
  },
  get "inspo:v6-wild"() {
    return tr("灵感生成 (inspo) · v6-wild");
  },
  get "inspo:v6-mini"() {
    return tr("灵感生成 (inspo) · v6-mini");
  },
  get "inspo:custom"() {
    return tr("灵感生成 (inspo) · 自定义模型");
  },
  get "mashup:v6-wild"() {
    return tr("生成混搭 (mashup) · v6-wild");
  },
  get "mashup:v6-mini"() {
    return tr("生成混搭 (mashup) · v6-mini");
  },
  get "mashup:custom"() {
    return tr("生成混搭 (mashup) · 自定义模型");
  },
  get "replaceMusic:v6-wild"() {
    return tr("段落替换 · v6-wild");
  },
  get "replaceMusic:v6-mini"() {
    return tr("段落替换 · v6-mini");
  },
  get "replaceMusic:custom"() {
    return tr("段落替换 · 自定义模型");
  },
  get "sample:v6-wild"() {
    return tr("样本转歌曲 · v6-wild");
  },
  get "sample:v6-mini"() {
    return tr("样本转歌曲 · v6-mini");
  },
  get "sample:custom"() {
    return tr("样本转歌曲 · 自定义模型");
  },
  get "sounds:v6-wild"() {
    return tr("音效生成 · v6-wild");
  },
  get "sounds:v6-mini"() {
    return tr("音效生成 · v6-mini");
  },
  get "upload_cover:v6-wild"() {
    return tr("上传音频翻唱 · v6-wild");
  },
  get "upload_cover:v6-mini"() {
    return tr("上传音频翻唱 · v6-mini");
  },
  get "upload_cover:custom"() {
    return tr("上传音频翻唱 · 自定义模型");
  },
  get "uploadExtend:v6-wild"() {
    return tr("上传音频并延伸 · v6-wild");
  },
  get "uploadExtend:v6-mini"() {
    return tr("上传音频并延伸 · v6-mini");
  },
  get "uploadExtend:custom"() {
    return tr("上传音频并延伸 · 自定义模型");
  }
};
export function musicPriceLabel(tier) {
  return musicPriceLabels[tier] || tier || tr("标准");
}

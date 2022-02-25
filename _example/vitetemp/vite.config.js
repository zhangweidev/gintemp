// vite.config.js
const { resolve } = require('path')
const { defineConfig } = require('vite')

import copy from 'rollup-plugin-copy'

module.exports = defineConfig({
  plugins:[
    copy({
      targets:[{
        src:['./layouts', './widgets'], 
        dest: '../dist/' 
      }
      ]
    })
  ],
  build: {
    rollupOptions: {
      input: {
        main: resolve(__dirname, 'index.html'),
        views: resolve(__dirname, 'views/login.html')
      }
    },
    outDir:"../dist", 
  }
})